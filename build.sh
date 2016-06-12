#!/bin/bash -ex

version=0.0.1

# cadvisor network hack
docker build -f Dockerfile.cadvisor -t rancher/haproxy-cadvisor:v$version .
docker tag rancher/haproxy-cadvisor:v$version rancher/haproxy-cadvisor
docker push rancher/haproxy-cadvisor
gsync rancher/haproxy-cadvisor:v$version james2 5

name=autoscale_build
docker build -f Dockerfile.build -t $name .
id=$(docker run -d --name $name $name sleep 15)
docker cp $name:/go/src/github.com/llparse/rancher-autoscale/rancher-autoscale .
docker rm -f $id

docker build -t rancher/autoscale:v$version .
docker tag rancher/autoscale:v$version rancher/autoscale
docker push rancher/autoscale
gsync rancher/autoscale:v$version james2 5
