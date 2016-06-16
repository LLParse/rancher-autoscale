#!/bin/bash -ex

version=v0.0.1

name=autoscale_build
docker build -f Dockerfile.build -t $name .
id=$(docker run -d --name $name $name sleep 15)
docker cp $name:/go/src/github.com/llparse/rancher-autoscale/rancher-autoscale .
docker rm -f $id

docker build -t rancher/autoscale:$version .
docker tag rancher/autoscale:$version rancher/autoscale
docker push rancher/autoscale:$version
docker push rancher/autoscale:latest
gsync rancher/autoscale:$version james 5
