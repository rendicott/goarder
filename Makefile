version := 0.3.19
package := goarder
packageName := ${package}-linux-amd64-$(version).tar.gz
packageNameLatest := ${package}-linux-amd64-latest.tar.gz
s3BucketPath := $(GOARDER_S3_BUCKET)/builds-goarder
s3BucketCredsProfile := $(GOARDER_CREDS_PROFILE)
build_dir := output

art: build upload
build: configure build-linux

configure:
	mkdir -p ${build_dir}

build-linux:
	@cd ./chook/ && make build version=${version}
	@cd ./ahoy/ && make build version=${version}
	tar -czf ./${build_dir}/${packageName} \
		./ahoy/ahoy.service \
		./ahoy/output-linux/ahoy \
		./chook/chook.service \
		./chook/output-linux/chook \
		./godocs/ge-go.png \
		./godocs/godocs.service \
		./godocs/redmarble.png \
		./awslogs.conf \
		./prep.sh

upload:
	aws --profile ${s3BucketCredsProfile} \
            s3 cp ./$(build_dir)/$(packageName) s3://${s3BucketPath}/
	aws --profile ${s3BucketCredsProfile} \
            s3 cp s3://${s3BucketPath}/${packageName} s3://${s3BucketPath}/${packageNameLatest}
