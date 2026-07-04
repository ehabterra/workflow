# A tiny, reliable Beanstalkd image built from Alpine's package (no dependency on
# an unmaintained third-party image). Beanstalkd is a single small binary whose
# whole job is to distribute work — exactly the "simple distributor" this example
# leans on.
FROM alpine:3.20
RUN apk add --no-cache beanstalkd
EXPOSE 11300
# -l 0.0.0.0 so the container accepts connections from the host.
ENTRYPOINT ["beanstalkd", "-l", "0.0.0.0", "-p", "11300"]
