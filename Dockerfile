# syntax=docker/dockerfile:1

FROM golang:1.19-alpine as build

RUN apk add --no-cache git make gcc musl-dev

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

RUN go build -tags musl --ldflags "-extldflags -static" -o server ./cmd/sfu

FROM alpine:3.17.0

RUN apk update && apk add tzdata

WORKDIR /app

COPY --from=build /app/sfu /app/sfu

EXPOSE 8888

CMD [ "./sfu" ]
