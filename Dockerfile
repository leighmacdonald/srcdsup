FROM golang:alpine as build
WORKDIR /build
COPY . .
RUN go build -o srcdsup

FROM alpine:latest
LABEL maintainer="Leigh MacDonald <leigh.macdonald@gmail.com>"
LABEL org.opencontainers.image.source="https://github.com/leighmacdonald/srcdsup"
RUN apk add dumb-init
WORKDIR /app
COPY --from=build /build/srcdsup .
ENTRYPOINT ["dumb-init", "--"]
CMD ["./srcdsup"]