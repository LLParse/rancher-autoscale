#!/bin/bash -ex

version=v0.0.1

# cadvisor network hack
docker build -t rancher/haproxy-cadvisor:$version .
docker tag rancher/haproxy-cadvisor:$version rancher/haproxy-cadvisor
docker push rancher/haproxy-cadvisor
