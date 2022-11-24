build:
	go build .

IMG ?= quay.io/mdewald/pod-autoscaler-example

docker-build:
	docker build . -t $(IMG)
docker-push:
	docker push $(IMG)
