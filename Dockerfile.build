FROM golang:alpine

ENV PROJECT github.com/llparse/rancher-autoscale
RUN apk update && apk add git
RUN go get github.com/Sirupsen/logrus
RUN go get github.com/golang/glog
RUN go get github.com/gorilla/websocket
#RUN go get github.com/google/cadvisor

RUN mkdir -p /go/src/$PROJECT
ADD . /go/src/$PROJECT
WORKDIR /go/src/$PROJECT
RUN go build
