#!/bin/bash -ex

version=0.0.1

name=autoscale_build
docker build -f Dockerfile.build -t $name .
id=$(docker run -d --name $name $name sleep 15)
docker cp $name:/go/src/github.com/llparse/rancher-autoscale/rancher-autoscale .
docker rm -f $id

docker build -t rancher/autoscale:v$version .
docker tag rancher/autoscale:v$version rancher/autoscale
docker push rancher/autoscale
gsync rancher/autoscale:v$version james 5
