FROM alpine:3.4

ADD rancher-autoscale /usr/bin/autoscale
ENTRYPOINT ["/usr/bin/autoscale"]
