FROM alpine:edge
RUN apk add --no-cache bpftrace
RUN apk del apk-tools
WORKDIR /root
COPY entrypoint.sh thist.bt .
ENTRYPOINT ["/root/entrypoint.sh"]
