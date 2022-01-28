package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
)

var version string

// conf holds config and exports for use in other
// packages
var conf *config

// config is an internal struct for storing
// configuration needed to run this application
// such as the DynamoDB table name and region
type config struct {
	GitHubPAT          string   `yaml:"github_pat"`
	GitHubServer       string   `yaml:"github_server"`
	DynamoDBRegion     string   `yaml:"dynamodb_region"`
	DynamoDBTable      string   `yaml:"dynamodb_table"`
	DynamoDBTriggerKey string   `yaml:"dynamodb_trigger_key"`
	Interval           int      `yaml:"interval"`
	GoGetEnvs          []string `yaml:"go_get_envs"`
	GoBinaryPath       string   `yaml:"go_binary_path"`
}

// loadConfigSecretsManager takes a secretname and loads it
// from secrets manager
func (c *config) loadConfigSecretsManager(secretName, secretRegion string) error {
	fmt.Println("attempting to load config from secrets manager")
	//Create a Secrets Manager client
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		Config: aws.Config{Region: aws.String(secretRegion)},
	}))
	svc := secretsmanager.New(sess)
	input := &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(secretName),
		VersionStage: aws.String("AWSCURRENT"), // VersionStage defaults to AWSCURRENT if unspecified
	}

	// // In this sample we only handle the specific exceptions for the 'GetSecretValue' API.
	// // See https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html

	result, err := svc.GetSecretValue(input)
	if err != nil {
		fmt.Printf("Error retrieving secret value. Error: '%s'\n", err.Error())
		return err
	}

	// Decrypts secret using the associated KMS CMK.
	// Depending on whether the secret is a string or binary, one of these fields will be populated.
	var secretString string
	if result.SecretString != nil {
		secretString = *result.SecretString
		err = yaml.Unmarshal([]byte(secretString), c)
		if err != nil {
			fmt.Printf("Error unmarshaling secret text into config object. Error: '%s'\n", err.Error())
			return err
		}
	}
	err = c.setConfigDefaults()
	return err
}

// setConfigDefaults examines current loaded config
// and sets defaults for any missing fields or returns
// an error if something is wrong with a field.
func (c *config) setConfigDefaults() (err error) {
	// set defaults
	if c.Interval == 0 {
		c.Interval = 20
	}
	fmt.Printf("Starting with config '%s = %d'\n", "Interval", c.Interval)

	if c.DynamoDBRegion == "" {
		c.DynamoDBRegion = "us-east-1"
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBRegion", c.DynamoDBRegion)

	if c.GoBinaryPath != "" {
		fmt.Printf("Starting with config '%s = %s'\n", "GoBinaryPath", c.GoBinaryPath)
	}

	if c.GitHubPAT == "" {
		c.GitHubPAT = ""
	}
	fmt.Printf("Starting with config '%s = [redacted] (but has length %d)'\n", "GitHubPAT", len(c.GitHubPAT))

	if c.GitHubServer == "" {
		err = errors.New("missing configuration directive github_server")
		return err
	}
	fmt.Printf("Starting with config '%s = %s'\n", "GitHubServer", c.GitHubServer)

	if c.DynamoDBTable == "" {
		err = errors.New("missing configuration directive dynamodb_table")
		return err
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBTable", c.DynamoDBTable)

	if c.DynamoDBTriggerKey == "" {
		c.DynamoDBTriggerKey = "00000trigger"
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBTriggerKey", c.DynamoDBTriggerKey)
	return err
}

// loadConfigFile takes a yaml filename as input and
// attempts to parse it into a config object.
func (c *config) loadConfigFile(filename string) (err error) {
	yamlFile, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		return err
	}
	err = c.setConfigDefaults()
	return err
}

// counter holds current synch state with table
var counter *int

// localRepos tracks known local repos so when delete
// events happen we know which repos to delete
var localRepos []string

type Trigger struct {
	Repo  string `json:"repo"`
	Count *int   `json:"count"`
}

func (t *Trigger) GetCounter() (err error) {
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return err
	}
	dsvc := dynamodb.New(sess)
	kvalue := make(map[string]*dynamodb.AttributeValue)
	kvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(conf.DynamoDBTriggerKey)}
	getItemInput := dynamodb.GetItemInput{
		Key:       kvalue,
		TableName: &conf.DynamoDBTable,
	}
	rvalue, err := dsvc.GetItem(&getItemInput)
	if err != nil {
		fmt.Println(err)
		return err
	}
	if val, ok := rvalue.Item["count"]; ok {
		cnt := 0
		err = dynamodbattribute.Unmarshal(val, &cnt)
		if err != nil {
			return err
		}
		fmt.Printf("Detected count as %d\n", cnt)
		t.Count = &cnt
	}
	return err
}

func AppCleanup() {
	fmt.Println("CLEANUP APP BEFORE EXIT!!!")
}

func getRepos() (repos []string, err error) {
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return repos, err
	}
	dsvc := dynamodb.New(sess)
	params := dynamodb.ScanInput{
		TableName: &conf.DynamoDBTable,
	}
	maxpages := 50
	// Example iterating over at most 3 pages of a Scan operation.
	pageNum := 0
	err = dsvc.ScanPages(&params,
		func(page *dynamodb.ScanOutput, lastPage bool) bool {
			pageNum++
			for _, item := range page.Items {
				if _, ok := item["repo"]; ok {
					repoName := *item["repo"].S
					if repoName != conf.DynamoDBTriggerKey {
						repos = append(repos, repoName)
					}
				}
			}
			return pageNum <= maxpages
		})
	return repos, err
}

func handle(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func manageGitconfig() {
	// Need to make sure the following is in the ~/.gitconfig before
	// trying any 'go get' commands

	// [url "https://aslkdflaknvakwnf1201alsdfa@github.build.company.com"]
	//        insteadOf = https://github.build.company.com
	if len(conf.GitHubPAT) < 1 {
		return
	}
	fmt.Println("setting up PAT for GitHub server")
	ghServerStringHttps1 := fmt.Sprintf(
		`[url "https://%s@%s"]`,
		conf.GitHubPAT, conf.GitHubServer,
	)
	ghServerStringHttps2 := fmt.Sprintf(
		`        insteadOf = https://%s`,
		conf.GitHubServer,
	)
	ghServerStringHttp1 := fmt.Sprintf(
		`[url "http://%s@%s"]`,
		conf.GitHubPAT, conf.GitHubServer,
	)
	ghServerStringHttp2 := fmt.Sprintf(
		`        insteadOf = http://%s`,
		conf.GitHubServer,
	)
	filename := "/etc/gitconfig"
	file, err := os.Create(filename)
	if err != nil {
		handle(err)
	}
	writer := bufio.NewWriter(file)
	linesToWrite := []string{
		ghServerStringHttps1,
		ghServerStringHttps2,
		ghServerStringHttp1,
		ghServerStringHttp2,
	}
	for _, line := range linesToWrite {
		_, err = writer.WriteString(line + "\n")
		if err != nil {
			handle(err)
		}
	}
	fmt.Printf("wrote %d lines to %s\n", len(linesToWrite), filename)
	writer.Flush()
}

func update() (err error) {
	repos, err := getRepos()
	if err != nil {
		return err
	}
	fmt.Print("done getting repos, 'go get'ting them and ignoring errors\n")
	for _, repo := range repos {
		fmt.Printf("performing 'go get -u -d' for repo '%s'\n", repo)
		var cmd *exec.Cmd
		if conf.GoBinaryPath == "" {
			cmd = exec.Command("go", "get", "-u", "-d", repo)
		} else {
			cmd = exec.Command(
				conf.GoBinaryPath, "get", "-u", "-d", repo,
			)
		}

		cmd.Env = os.Environ()
		for _, env := range conf.GoGetEnvs {
			cmd.Env = append(cmd.Env, env)
		}
		cmd.Stderr = os.Stdout
		out, err := cmd.Output()
		if err != nil {
			fmt.Printf("len(out) = %d, got error: '%s'\n", len(out), err.Error())
			if out != nil {
				fmt.Println(string(out))
			}
		}
		// add to a local copy so we can compare later for deletion
		localRepos = append(localRepos, repo)

	}
	// now check to see if any previous repos are now missing from list
	var reposToDelete []string
	for _, lrepo := range localRepos {
		missing := true
		for _, r := range repos {
			if r == lrepo {
				missing = false
			}
		}
		if missing {
			fmt.Printf("Adding repo to delete '%s'\n", lrepo)
			reposToDelete = append(reposToDelete, lrepo)
		}
	}
	// now delete them
	for _, repo := range reposToDelete {
		fullPathToDelete := fmt.Sprintf("src/%s", repo)
		fmt.Printf("attempting to perform  'rm -rf' for local source of '%s/%s'\n", "$GOPATH", fullPathToDelete)
		var cmd *exec.Cmd
		cmd = exec.Command("rm", "-rf", "-d", fullPathToDelete)

		cmd.Env = os.Environ()
		fmt.Println("attempting to perform GOPATH expansion and cmd injection")
		var gopath string
		for _, env := range conf.GoGetEnvs {
			fmt.Printf("Have env of '%s'\n", env)
			chunked := strings.Split(env, "=")
			if len(chunked) > 1 {
				if chunked[0] == "GOPATH" {
					fmt.Println("found GOPATH env var")
					gopath = chunked[1]
				}
			}
			cmd.Env = append(cmd.Env, env)
		}
		if len(gopath) > 0 {
			finalPath := fmt.Sprintf("%s/%s", gopath, fullPathToDelete)
			cmd.Args[3] = finalPath
			fmt.Printf("Got full path embedded in cmd of '%s'\n", finalPath)
		} else {
			err = errors.New("GOPATH env var empty unable to determine full delete path")
		}
		if err != nil {
			return err
		}
		cmd.Stderr = os.Stdout
		out, err := cmd.Output()
		if err != nil {
			fmt.Printf("len(out) = %d, got error: '%s'\n", len(out), err.Error())
			if out != nil {
				fmt.Println(string(out))
			}
		}
	}
	// requires user running 'ahoy' can control this service
	// example in sudoers file:
	//  Cmnd_Alias GOARDER_CMNDS = /bin/systemctl start godocs, /bin/systemctl stop godocs, /bin/systemctl restart godocs.service
	//  %goarder ALL=(ALL) NOPASSWD: GOARDER_CMNDS
	exec.Command("sudo", "/bin/systemctl", "restart", "godocs.service").Output()
	return err
}

func main() {
	c := config{}
	conf = &c
	var configFile string
	var secretName string
	var secretRegion string
	var versionFlag bool
	flag.StringVar(&configFile, "config", "/etc/chook.yml", "Filename of YAML configuration file.")
	flag.StringVar(&secretName, "s", "ahoy-config", "to load config from AWS secrets manager provide the name of secret in secrets manager")
	flag.StringVar(&secretRegion, "r", "us-east-1", "to load config from AWS secrets manager provide the region where the secret is stored")
	flag.BoolVar(&versionFlag, "v", false, "print version and exit")
	flag.Parse()
	if versionFlag {
		fmt.Printf("ahoy %s\n", version)
		os.Exit(0)
	}
	fmt.Printf("ahoy %s\n", version)
	// process config. First try to load from config file
	// if that fails load from secrets manager
	err := c.loadConfigFile(configFile)
	if err != nil {
		fmt.Printf("Unable to load config from file. Error: '%s'\n", err.Error())
		err := c.loadConfigSecretsManager(secretName, secretRegion)
		if err != nil {
			fmt.Printf("Unable to load config from secrets manager. Error: '%s'\n", err.Error())
			os.Exit(1)
		}
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	starter := 0
	counter = &starter
	go sigCatcher(sigs)
	manageGitconfig()
	for {
		var t Trigger
		// wake up, check trigger
		t.Count = &[]int{0}[0]
		err := t.GetCounter()
		if err != nil {
			fmt.Printf("Fatal error, exiting: %s\n", err.Error())
			os.Exit(1)
		}
		if *counter != *t.Count {
			// if counter is diff then we update
			err = update()
			if err != nil {
				fmt.Printf("Fatal error, exiting: %s\n", err.Error())
				os.Exit(1)
			}
			counter = t.Count
			fmt.Printf("set new local counter to %d\n", *counter)
		} else {
			// otherwise go back to sleep
			fmt.Println("nothing to do, sleeping")
		}
		fmt.Printf("sleeping %ds before checking for updates\n", conf.Interval)
		time.Sleep(time.Millisecond * time.Duration(conf.Interval*1000))
	}
}

// sigCatcher waits for os signals to terminate gracefully
// after it receives a signal on the sigs channel.
// main() waits for a bool on the done channel.
func sigCatcher(sigs chan os.Signal) {
	sig := <-sigs
	fmt.Printf("received signal '%s'\n", sig)
	AppCleanup()
	os.Exit(1)
}
