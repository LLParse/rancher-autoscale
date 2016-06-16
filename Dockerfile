FROM alpine:3.4

ADD rancher-autoscale /usr/bin/autoscale
ADD entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
