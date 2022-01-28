# goarder

goarder is a way to set up a local godocs server so you can serve up godocs from your private GitHub Enterprise server. It is designed to be run within AWS infrastructure on an EC2 instance but could be modified to run elsewhere with some work.

## How It Works
If you want to add a package to the godoc server:
1. Find the repo that hosts the package or module that you want to publish godocs for.
2. Add a webhook of `https://{chook-URL}/hook` in `application/json` format to that repo.
3. Send link to friend.

If you want to remove a package from the godoc server:
1. Find the repo that hosts the package or module that you want to publish godocs for.
2. Modify the webhook from `https://{chook-URL}/hook` to `https://{chook-URL}/delete`.
3. Go to godoc server URL and validate it was removed.


## Overview
Goarder is a collection of services designed to work in concert as a web server that accepts incoming webhooks from a private GitHub server and then serve up the contents of those repositories as godocs. 

It's made up of a few services meant to be deployed to a server or set of servers.

* `chook` listens for webhooks from your GitHub server and updates a DynamoDB table with repos
* `ahoy` continuously scans the DynamoDB table and updates local repos on the server.
* `godocs` runs a godocs server from the same server as `ahoy`

Below is a more detailed explanation of each component.

### godocs
Godocs is a service wrapper around the pre-existing `godocs` [package](https://godoc.org/golang.org/x/tools/cmd/godoc). This serves up an HTTP server with documentation for all of the packages in the local server's target folder. 

### chook
Chook is a web server that accepts incoming Github webhooks from a repository and then adds information about that repository to a DynamoDB table. It then updates a counter so that anything consuming the table knows to do a rescan of the table and pull all of the latest godocs. It also has a delete handler so that you can remove entries from the table. 

### ahoy
ahoy is a daemon that scans the DynamoDB table at an interval to determine whether or not to pull the latest packages down so that the godocs server can serve them. When it sees that there is an update to the table it rescans the table and does a `go get -ud <package>` on all of the repos in the table. When it detects that a package was removed it removes that collection of files from the filesystem. 

# Setup 
This section will cover two ways of deploying the service--manual and via the cloudformation template. 

## Prereqs
Regardless of whether you do a manual install of each component or use the cloudformation template you'll need to set a few things ahead of time. 

### DynamoDB Table
You'll need to create a DynamoDB table to store the information about which repositories to display on the godocs server. You should create this in the same account in which your infrastructure will run. 

Below is the command to create the table and set the base counter value. The counter will be used by the application to know when to trigger `go get` on each repo. 
```bash
aws dynamodb create-table \
	--table-name goarder-stage \
	--attribute-definitions AttributeName=repo,AttributeType=S \
	--key-schema AttributeName=repo,KeyType=HASH \
	--billing-mode PAY_PER_REQUEST
aws dynamodb put-item \
	--table-name goarder-stage \
	--item '{"repo": { "S": "00000trigger" }, "count" : { "N": "0" }}'
```

Remember the name of the table you created for when you're building your configuration for `ahoy` and `chook`.

### Github Access
You are asked to provide a Github personal access token to the `ahoy` service so that it can pull code from your private repositories. You can read Github documentation to learn how to generate this but the gist is that you log in as whatever user you want to grant the access for and you go to the user settings > Developer settings > Personal Access Tokens and generate one from there.

Store this for later use when you're building the configuration for the `ahoy` service. 

### Build Config Files
Whether you're pushing config files to the server directly or storing in secrets manager we still neeed to build the config. Here's example configs for `ahoy` and `chook`. For more in depth explanations of each field see the sample configuration files within those sections of the project. 

chook-config.yml
```yaml
# chook config file
listen_string: 0.0.0.0:5050
dynamodb_region: us-east-1
dynamodb_table: goarder-stage
dynamodb_trigger_key: 00000trigger
```

ahoy-config.yml
```yaml
# ahoy config file
interval: 20 # seconds between trigger checks
dynamodb_region: us-east-1
dynamodb_table: goarder-stage
github_server: my.github.company.com # hostname of private github server
github_pat: AWOGOIAOCOKOWAQKVJBKELKALJKSEK # personal access token for GH server
go_get_envs:
  - "GOPATH=/tmp/source" # location on server where `go get` will store
  - "GOSUMDB=off" # if you want to disable module checksums
  - "GOPROXY=direct" # if you want to disable module mothership checks
go_binary_path: /usr/local/go/bin/go # path to go binary on server
```

Save these files for later use.

### Secrets Manager Secret (Optional)
If you'd prefer to keep your application configuration stored in secrets manager so you don't have to worry about distributing configuration with senstive information (Github PAT) to your server then it's recommended to store the configuration in Secrets Manager. 

NOTE: This step is mandatory if you're using the provided cloudformation template. 

You can use the below snippet to set up your inital secrets:

```bash
aws secretsmanager create-secret \
	--name ahoy-stage \
	--secret-string file:\\ahoy-config.yml
```

```bash
aws secretsmanager create-secret \
	--name chook-stage \
	--secret-string file:\\chook-config.yml
```

Take note of the Secret ARNs for later use.

## Manual Services Setup
(you can skip this section if you're using the cloudformation template)

This section will cover the manual steps required to install each individual service in case you want to break up the services onto separate servers or just set them up on a test machine. 

Ahoy and the Godocs binary need to run on the same machine or at least share a filesystem. Chook can run on the same server or it can run on its own on a container or something. For this guide we'll just put them all on a single server. 

#### Chook
First start by unpacking the release tarball and extracting the `chook` binary and copy it to your server along with the config file you created earlier. You can run it directly by calling `./chook -config config.yml` and it should start up a webserver listening on whatever port you have defined in the config file. See the [sample config](./chook/config_sample.yml) for more info.

If you're running an OS with systemd you can use the `chook.service` to set it up as a service. 
1. Make sure you properly reference your config file location or set up the server to access the config via secrets manager.
1. Copy `chook.service` to `/usr/lib/systemd/system/` and then run `systemctl enable chook.service`. 
1. Make sure to create a user `chook` and group `goarder`. 
1. From there you can start/stop/restart the chook service just like any other service on your system.
1. Watch the system log for errors (e.g., `/var/log/messages`)

(you can reference relevant `chook` sections in `prep.sh` for help)

#### Ahoy
First start by unpacking the release tarball and extracting the `ahoy` binary and copy it to your server along with the config file you created earlier. You can run it directly by calling `./ahoy -config config.yml` and it should start up a daemon that monitors the dynamodb table you have in the config file and clones packages to your configured directory. See the [sample config](./ahoy/config_sample.yml) for more info.

If you're running an OS with systemd you can use the `ahoy.service` to set it up as a service. 
1. Make sure you properly reference your config file location or set up the server to access the config via secrets manager.
1. Copy `ahoy.service` to `/usr/lib/systemd/system/` and then run `systemctl enable ahoy.service`. 
1. Make sure to create a user `ahoy` and group `goarder`. 
1. From there you can start/stop/restart the ahoy service just like any other service on your system.
1. Watch the system log for errors (e.g., `/var/log/messages`)

(you can reference relevant `ahoy` sections in `prep.sh` for help)

#### godocs
The final piece is to run the `godocs` [binary](https://godoc.org/golang.org/x/tools/cmd/godoc) and serve up godocs out of the directory that `ahoy` populates. This is pretty straighforward but the package maintainers don't provide an RPM or a service wrapper for it. All that is provided for you here is a `godocs.service` file that can help you run `godocs` as a service on the same machine as `ahoy`.

If you want to run it as a service using the provided `godocs.service` file you'll have to do the following:
1. Make sure there's a `godocs` user on the system in the `goarder` group.
1. Make sure you have the ahoy config GOPATH (e.g., `go_get_envs: "GOPATH=/tmp/source"` ) set to the same as the `Environment="GOPATH=/tmp/source"` directive in the `godocs.service` file.
1. Enable the service with `systemctl enable godocs.service`
1. Start the service with `service godocs start`
1. Watch the system log for errors (e.g., `/var/log/messages`)

Also included in the godocs [folder](./godocs/) is a favicon and a logo png that can be placed in the `$GOPATH/favicon` and `$GOPATH/doc/gopher/pkg.png` paths respectively. This will prevent lots of warnings from spitting out from the godocs binary when it serves content. 

At this point you should have all the basic components to run the system. Please refer to the below "Accept Traffic and Troubleshoot" section for next steps.

## Automatic Deployment
(skip this if you did Manual setup)

You can choose to use the cloudformation template `cf.template` to deploy the infrastructure for the service. However, there are a few prerequisites required. 

1. First, make sure you've done the following prereqs as defined in this README for
   * Manually create DynamoDB Table
   * Manually create Secrets Manager secrets with the config. (take note of the ARNs)
1. Next, upload a package tar.gz from the Releases tab to an S3 bucket in your account.
1. Make sure you have your certificate uploaded to ACM for the certificate you'd like to put on the load balancer.
1. Pick out your subnets and VPC id ahead of time. 
   * Whatever subnets you pick have to be able to reach your private Github server.
   * Whatever subnets you pick also have to be reachable _from_ your private Github server so you can receive webhooks.
   * Whatever subnets you pick must be reachable by your clients on port :443 so they can view your godocs.

Next you're ready for a deploy attempt...
1. Go to Cloudformation in your account and create a new stack.
1. Upload the `cf.template` file and hit next.
1. Fill out all of the parameters you collected above. 
1. Launch stack. Pray.

NOTE: You can launch stack automatically with a parameters file:

```
aws --profile $GOARDER_CREDS_PROFILE cloudformation create-stack --stack-name goarder-stage2 --template-body file://cf.template --parameters file://../goarder-deploy/params-stage.json --capabilities CAPABILITY_IAM
```

## Accept Traffic and Troubleshoot
Now that you have `chook` listening for Github webhooks you can go to your Github server and test it out. Add a webhook to a repository that has go packages in it. It will look like `http://<your-web-server-address>:<chook-listen-port>/hook`. You should be able to see in the system log that an event was received. 

You can check your DynamoDB table to make sure a new entry was added. 

If `ahoy` is working correctly it will wake up periodically and read the DynamoDB table for new entries. If it finds one you should see it doing work in the system log. If `ahoy` is having trouble pulling your repos you can check to make sure that the server can reach the private Github URL configured in the config and that it has an appropriate Github PAT to authenticate. 

After `ahoy` successfully pulls down the packages you should be able to visit `http://<your-web-server-address>:<godocs-listen-port>` and you should see your godocs server display packages. At this point you may see some complaints in the log file about missing favicon and a missing `docs` folder. These are merely warnings but if you want a favicon and to stop the annoying messages you can stub out the docs directory it's complaining about. You can see more examples of how to resolve this in the `prep.sh` script.

If you use the cloudformation template the servers log their `/var/log/messages` automatically to a CloudWatch Logs group. 

Keep in mind that until you set up your first webhook ahoy won't have any packages to pull down so godocs service won't be able to start. You can see godocs complaining about this in `/var/log/messages` as "fstree empty" or something like that. This will keep the listener from launching on 8443 or whatever port you choose until you can get some packages downloaded. 

## Building Yourself
You can modify the code and build yourself. There's a Makefile in the top level directory that will go into the ahoy and chook folders and run those Makefiles then package everything up into an output folder.

If you want there's also a `make art` function that will upload the tarball to your S3 bucket but you have to have the `GOARDER_S3_BUCKET` and `GOARDER_CREDS_PROFILE` env vars set. 

# TODO
* RPMs?
