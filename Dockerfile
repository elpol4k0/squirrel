FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY squirrel /usr/local/bin/squirrel
ENTRYPOINT ["squirrel"]
