#!/bin/bash
set -e
yum update -y
GOBINARY="/usr/local/go/bin/go"
if [ ! -f "$GOBINARY" ]; then
        wget https://dl.google.com/go/go1.14.4.linux-amd64.tar.gz
        tar -C /usr/local -xzf go1.14.4.linux-amd64.tar.gz
fi
git version || yum install git -y
export PATH=$PATH:/usr/local/go/bin
export GOPATH=/root/go/
export GOCACHE=/root/go/
go get golang.org/x/tools/cmd/godoc
cp /root/go/bin/godoc /usr/local/go/bin/

USER1="godocs"
USER2="ahoy"
USER3="chook"
GROUP="goarder"
DIR="/srv/godocs"
# create group if no exist
getent group $GROUP &>/dev/null || groupadd $GROUP
# create user if not exist
id -u $USER1 &>/dev/null || useradd $USER1 -g $GROUP
id -u $USER2 &>/dev/null || useradd $USER2 -g $GROUP
id -u $USER3 &>/dev/null || useradd $USER3 -g $GROUP

mkdir -p $DIR

pushd /tmp/goarder
cp ./chook/output-linux/chook /usr/local/bin/
cp ./chook/chook.service /usr/lib/systemd/system/

cp ./ahoy/output-linux/ahoy /usr/local/bin/
cp ./ahoy/ahoy.service /usr/lib/systemd/system/

touch /etc/gitconfig
chown root:$GROUP /etc/gitconfig
chmod 664 /etc/gitconfig

cp ./godocs/godocs.service /usr/lib/systemd/system/
cp ./godocs/redmarble.png ${DIR}/favicon.ico
mkdir -p ${DIR}/doc/gopher
cp ./godocs/gopher.png ${DIR}/doc/gopher/pkg.png

popd

chown $USER1:$GROUP $DIR
chmod -R 775 $DIR

cp /etc/sudoers /etc/sudoers.new
echo "Cmnd_Alias GOARDER_CMNDS = /bin/systemctl start godocs, /bin/systemctl stop godocs, /bin/systemctl restart godocs.service" >> /etc/sudoers.new
echo "%goarder ALL=(ALL) NOPASSWD: GOARDER_CMNDS" >> /etc/sudoers.new
if visudo -cf /etc/sudoers.new; then
	cp /etc/sudoers /etc/sudoers.bak
	cp /etc/sudoers.new /etc/sudoers
fi

systemctl enable chook.service
systemctl enable ahoy.service
systemctl enable godocs.service
systemctl start chook
systemctl start ahoy
systemctl start godocs

# setup cloudwatch logs
yum install -y awslogs
cp /tmp/goarder/awslogs.conf /etc/awslogs/awslogs.conf
region=$(curl -s http://169.254.169.254/latest/dynamic/instance-identity/document | grep "region" | cut -d ":" -f 2 | cut -d \" -f 2)
sed -i s/us-east-1/$region/g /etc/awslogs/awscli.conf
sudo systemctl enable awslogsd.service
sudo systemctl start awslogsd
sleep 15
# set retention policy on cloudwatch logs group
aws logs put-retention-policy \
       --log-group-name /goarder/var/log/messages \
       --region $region \
       --retention-in-days 30

