FROM golang:latest AS builder
COPY . /src
WORKDIR /src
RUN CGO_ENABLED=0 go build
RUN ls

FROM scratch
COPY --from=builder /src/pod-autoscaler-example /
EXPOSE 8081
WORKDIR /
ENTRYPOINT ["/pod-autoscaler-example"]
