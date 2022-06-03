FROM golang:alpine as build
WORKDIR /build
COPY . .
RUN go build -o stvup

FROM alpine:latest
LABEL maintainer="Leigh MacDonald <leigh.macdonald@gmail.com>"
RUN apk add dumb-init
WORKDIR /app
COPY --from=build /build/stvup .
ENTRYPOINT ["dumb-init", "--"]
CMD ["./stvup"]