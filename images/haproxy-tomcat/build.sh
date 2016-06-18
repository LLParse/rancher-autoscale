#!/bin/bash -ex

version=v0.0.1
image=haproxy-tomcat

docker build -t rancher/$image:$version .
docker tag rancher/$image:$version rancher/$image
docker push rancher/$image
