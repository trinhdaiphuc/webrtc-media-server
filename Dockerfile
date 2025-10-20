# syntax=docker/dockerfile:1

FROM golang:1.22-alpine3.20 AS build

RUN apk add --no-cache git make gcc musl-dev

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

RUN go build -tags musl --ldflags "-extldflags -static" -o sfu ./cmd/sfu

FROM alpine:3.22.2

RUN apk update && apk add tzdata

WORKDIR /app

COPY --from=build /app/sfu /app/sfu

EXPOSE 8888

CMD [ "./sfu" ]
