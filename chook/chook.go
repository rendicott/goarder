package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
)

// conf holds config and exports for use in other
// packages
var conf *config

// config is an internal struct for storing
// configuration needed to run this application
// such as the DynamoDB table name and region
type config struct {
	ListenString       string `yaml:"listen_string"`
	DynamoDBRegion     string `yaml:"dynamodb_region"`
	DynamoDBTable      string `yaml:"dynamodb_table"`
	DynamoDBtriggerKey string `yaml:"dynamodb_trigger_key"`
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
			fmt.Printf("Error unmarshaling secret text into Config object. Error: '%s'\n", err.Error())
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
	if c.ListenString == "" {
		c.ListenString = ":5050"
	}
	fmt.Printf("Starting with config '%s = %s'\n", "ListenString", c.ListenString)

	if c.DynamoDBRegion == "" {
		c.DynamoDBRegion = "us-east-1"
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBRegion", c.DynamoDBRegion)

	if c.DynamoDBTable == "" {
		err = errors.New("missing configuration directive dynamodb_table")
		return err
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBTable", c.DynamoDBTable)

	if c.DynamoDBtriggerKey == "" {
		c.DynamoDBtriggerKey = "00000trigger"
	}
	fmt.Printf("Starting with config '%s = %s'\n", "DynamoDBtriggerKey", c.DynamoDBtriggerKey)

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

type author struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type headCommit struct {
	Id      string `json:"id"`
	Message string `json:"message"`
	Author  author `json:"author"`
}

type repository struct {
	SVNURL   string `json:"svn_url"`
	SSHURL   string `json:"ssh_url"`
	CloneURL string `json:"clone_url"`
	GITURL   string `json:"git_url"`
}

type githubWebhook struct {
	Ref        string     `json:"ref"`
	Repository repository `json:"repository"`
	HeadCommit headCommit `json:"head_commit"`
	Repo       string
}

type trigger struct {
	Repo  string `json:"repo"`
	Count *int   `json:"count"`
}

func (t *trigger) dynamoFormat() map[string]*dynamodb.AttributeValue {
	countString := strconv.Itoa(*t.Count)
	rvalue := make(map[string]*dynamodb.AttributeValue)
	rvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(t.Repo)}
	rvalue["count"] = &dynamodb.AttributeValue{
		N: &countString}
	return rvalue
}

func (t *trigger) getCounter() (err error) {
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return err
	}
	dsvc := dynamodb.New(sess)
	kvalue := make(map[string]*dynamodb.AttributeValue)
	kvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(conf.DynamoDBtriggerKey)}
	getItemInput := dynamodb.GetItemInput{
		Key:       kvalue,
		TableName: &conf.DynamoDBTable,
	}
	rvalue, err := dsvc.GetItem(&getItemInput)
	if err != nil {
		return err
	}
	if val, ok := rvalue.Item["count"]; ok {
		cnt := 0
		err = dynamodbattribute.Unmarshal(val, &cnt)
		if err != nil {
			return err
		}
		fmt.Printf("Detected count as %d\n", cnt)
		tcount := cnt + 1
		t.Count = &tcount
		fmt.Printf("Updating new count: %d\n", *t.Count)
		t.Repo = conf.DynamoDBtriggerKey
		err = t.writeDynamo()
		if err != nil {
			return err
		}
	}
	return err
}

func (t *trigger) writeDynamo() (err error) {
	fmt.Printf("In writedynamo i have cout of %d\n", *t.Count)
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return err
	}
	dsvc := dynamodb.New(sess)
	ddbItem := t.dynamoFormat()
	input := dynamodb.PutItemInput{
		TableName: &conf.DynamoDBTable,
		Item:      ddbItem,
	}
	fmt.Println("Attempting to write new trigger")
	_, err = dsvc.PutItem(&input)
	return err
}

// Sets the go get repo name by parsing the URL
func (g *githubWebhook) setRepo() (err error) {
	chunks := strings.Split(g.Repository.SVNURL, "//")
	fmt.Println(g.Repository.SVNURL)
	if len(chunks) > 1 {
		g.Repo = chunks[1]
	} else {
		err = errors.New("error parsing URL to build go get repo name")
	}
	return err
}

func (g *githubWebhook) dynamoFormat() map[string]*dynamodb.AttributeValue {
	rvalue := make(map[string]*dynamodb.AttributeValue)
	rvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(g.Repo)}
	rvalue["lastCommitId"] = &dynamodb.AttributeValue{
		S: aws.String(g.HeadCommit.Id)}
	rvalue["lastCommitMessage"] = &dynamodb.AttributeValue{
		S: aws.String(g.HeadCommit.Message)}
	rvalue["lastCommitUser"] = &dynamodb.AttributeValue{
		S: aws.String(g.HeadCommit.Author.Email)}
	return rvalue
}

func (g *githubWebhook) deleteDynamo() (err error) {
	// first we need to do a getItem so we know the whole schema
	// of the current key
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return err
	}
	dsvc := dynamodb.New(sess)
	// prep for getitem of current hook key
	kvalue := make(map[string]*dynamodb.AttributeValue)
	kvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(g.Repo)}
	getItemInput := dynamodb.GetItemInput{
		Key:       kvalue,
		TableName: &conf.DynamoDBTable,
	}
	_, err = dsvc.GetItem(&getItemInput)
	if err != nil {
		return err
	}
	// now that we have the exact object we can delete it
	fmt.Printf("found repo '%s' in table, now deleting...\n", g.Repo)
	dvalue := make(map[string]*dynamodb.AttributeValue)
	dvalue["repo"] = &dynamodb.AttributeValue{
		S: aws.String(g.Repo)}
	input := dynamodb.DeleteItemInput{
		TableName: &conf.DynamoDBTable,
		Key:       dvalue,
	}
	_, err = dsvc.DeleteItem(&input)
	return err

}

func (g *githubWebhook) writeDynamo(method string) (err error) {
	if g.Repo == conf.DynamoDBtriggerKey {
		// protect the trigger key since users can control these writes
		err = errors.New("cannot delete trigger key with hook methods")
		return err
	}
	sess, err := session.NewSession(
		&aws.Config{Region: aws.String(conf.DynamoDBRegion)},
	)
	if err != nil {
		return err
	}
	dsvc := dynamodb.New(sess)
	ddbItem := g.dynamoFormat()
	if g.Repo == conf.DynamoDBtriggerKey {
		err = errors.New("cannot delete trigger key with hook methods")
	}
	if method == "create" {
		input := dynamodb.PutItemInput{
			TableName: &conf.DynamoDBTable,
			Item:      ddbItem,
		}
		_, err = dsvc.PutItem(&input)
	} else if method == "delete" {
		err = g.deleteDynamo()
	} else {
		err = errors.New(fmt.Sprintf("unknown method '%s'", method))
	}
	return err
}

func healthcheck(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Healthy!")
}

func handlerCreate(w http.ResponseWriter, r *http.Request) {
	del := false
	handlerCreateDelete(w, r, del)
}

func handlerDelete(w http.ResponseWriter, r *http.Request) {
	del := true
	handlerCreateDelete(w, r, del)
}

func handlerCreateDelete(w http.ResponseWriter, r *http.Request, del bool) {
	switch r.Method {
	case "POST":
		buf := new(bytes.Buffer)
		bodyBytes, err := ioutil.ReadAll(r.Body)
		buf.ReadFrom(r.Body)
		newStr := buf.String()
		if err != nil {
			http.Error(w, "could not read body", http.StatusInternalServerError)
			return
		}
		fmt.Printf(newStr)
		fmt.Println()
		// try to parse webook to struct
		var hook githubWebhook
		json.Unmarshal(bodyBytes, &hook)
		if len(hook.Repository.SVNURL) > 0 {
			fmt.Println("parsed hook:")
			fmt.Printf("\tRef: %s\n", hook.Ref)
			fmt.Printf("\tAuthor: %s\n", hook.HeadCommit.Author.Name)
			fmt.Printf("\tCommitId: %s\n", hook.HeadCommit.Id)
			fmt.Printf("\tMessage: %s\n", hook.HeadCommit.Message)
			err = hook.setRepo()
			if err != nil {
				http.Error(w, "could not parse repo name", http.StatusInternalServerError)
				fmt.Printf("Error parsing repo name: %s\n", err.Error())
				return
			}
			fmt.Printf("\tRepo: %s\n", hook.Repo)
			if del {
				method := "delete"
				err = hook.writeDynamo(method)
				if err != nil {
					http.Error(w, "dynamo delete error", http.StatusInternalServerError)
					fmt.Println(err.Error())
					return
				}
				fmt.Printf("delete successful for repo '%s'\n", hook.Repo)
			} else {
				method := "create"
				err = hook.writeDynamo(method)
				if err != nil {
					http.Error(w, "dynamo create error", http.StatusInternalServerError)
					fmt.Println(err.Error())
					return
				}
				fmt.Printf("create successful for repo '%s'\n", hook.Repo)
			}
			// now update trigger
			var t trigger
			t.Count = &[]int{0}[0]
			err = t.getCounter()
			if err != nil {
				http.Error(w, "error retreiving trigger value", http.StatusInternalServerError)
				fmt.Println(err.Error())
				return
			}
		} else {
			fmt.Println("failure to parse hook")
			fmt.Println(hook)
		}
		fmt.Println()
	}
}

var version string

func main() {
	c := config{}
	conf = &c
	var configFile string
	var secretName string
	var secretRegion string
	var versionFlag bool
	flag.StringVar(&configFile, "config", "/etc/chook.yml", "Filename of YAML configuration file. Will attempt to load from file first then failover to secrets manager.")
	flag.StringVar(&secretName, "s", "chook-config", "to load config from AWS secrets manager provide the name of secret in secrets manager")
	flag.StringVar(&secretRegion, "r", "us-east-1", "to load config from AWS secrets manager provide the region where the secret is stored")
	flag.BoolVar(&versionFlag, "v", false, "print version and exit")
	flag.Parse()
	if versionFlag {
		fmt.Printf("chook%s\n", version)
		os.Exit(0)
	}
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

	// handle route using handler function
	http.HandleFunc("/hook", handlerCreate)
	http.HandleFunc("/delete", handlerDelete)
	http.HandleFunc("/", healthcheck)

	// listen to port
	http.ListenAndServe(conf.ListenString, nil)
}
