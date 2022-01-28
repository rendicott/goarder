version?=0.0.3
package := ahoy
deployHost := godoc-dev2
deployPath := /home/ec2-user/goarder/${package}/
s3BucketPath := ge-digital-public-cloudops-builds/builds-goarder
s3BucketCredsProfile := digital-public-cloudops
packageNameNix := ${package}-linux-amd64-$(version).tar.gz
packageNameNixLatest := ${package}-linux-amd64-latest.tar.gz

build_dir := output
build_dir_linux := output-linux

artifact: build upload
build: clean format configure build-linux
deploy: build deploy-host

format:
	go fmt ./...


configure:
		mkdir -p $(build_dir)
		mkdir -p $(build_dir_linux)

build-linux:
		env GOOS=linux GOARCH=amd64 go build -o ./$(build_dir_linux)/${package} -ldflags "-X main.version=$(version)"
		@cd ./$(build_dir_linux) && tar zcf ../$(build_dir)/$(packageNameNix) .

clean:
		rm -rf $(build_dir)
		rm -rf $(build_dir_linux)

upload:
		aws --profile ${s3BucketCredsProfile} \
			s3 cp ./$(build_dir)/$(packageNameNix) s3://${s3BucketPath}/
		aws --profile ${s3BucketCredsProfile} \
			s3 cp s3://${s3BucketPath}/${packageNameNix} s3://${s3BucketPath}/${packageNameNixLatest}

deploy-host:
	scp ./output-linux/${package} ${deployHost}:${deployPath}/${package}
