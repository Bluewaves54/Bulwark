FROM alpine:3.21
RUN apk add --no-cache curl jq bash
RUN adduser -D -u 1001 tester
USER tester
WORKDIR /home/tester
COPY test.sh /home/tester/test.sh
ENTRYPOINT ["bash", "/home/tester/test.sh"]
