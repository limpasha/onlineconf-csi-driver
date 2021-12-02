FROM golang

WORKDIR /go/src

COPY go.* ./
RUN go mod download

COPY cmd/*.go ./
RUN go build -o universal-csi-driver

FROM gcr.io/distroless/base

COPY --from=0 /go/src/universal-csi-driver /usr/local/bin/universal-csi-driver

ENTRYPOINT ["universal-csi-driver"]