#!/bin/bash -ex

version=0.0.1

# cadvisor network hack
docker build -f Dockerfile.cadvisor -t rancher/haproxy-cadvisor:v$version .
docker tag rancher/haproxy-cadvisor:v$version rancher/haproxy-cadvisor
docker push rancher/haproxy-cadvisor
gsync rancher/haproxy-cadvisor:v$version james2 5

