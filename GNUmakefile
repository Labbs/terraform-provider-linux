default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

# Acceptance tests against a throwaway Debian host running systemd, reached
# over SSH as a user that needs a password for sudo (see testenv/). The
# container is privileged so that tests can change kernel parameters and the
# host name. With Docker: make testenv-up TESTENV_ENGINE=docker \
#   TESTENV_RUN_FLAGS="--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw"
TESTENV_ENGINE ?= podman
TESTENV_RUN_FLAGS ?= --privileged
TESTENV_NAME = terraform-provider-linux-testenv

testenv-up:
	$(TESTENV_ENGINE) build -t $(TESTENV_NAME) -f testenv/Containerfile testenv
	$(TESTENV_ENGINE) run -d --name $(TESTENV_NAME) -p 127.0.0.1:2222:22 $(TESTENV_RUN_FLAGS) $(TESTENV_NAME)
	@for i in $$(seq 30); do \
		$(TESTENV_ENGINE) exec $(TESTENV_NAME) systemctl is-active --quiet ssh && exit 0; \
		sleep 1; \
	done; echo "sshd did not start" >&2; exit 1

testenv-down:
	$(TESTENV_ENGINE) rm -f $(TESTENV_NAME)

testacc-ssh:
	TF_ACC=1 LINUX_MODE=ssh LINUX_SSH_HOST=127.0.0.1 LINUX_SSH_PORT=2222 \
	LINUX_SSH_USER=deploy LINUX_SSH_PASSWORD=deploy LINUX_SSH_INSECURE_IGNORE_HOST_KEY=true \
	LINUX_SUDO=true LINUX_SUDO_PASSWORD=deploy LINUX_ACC_SYSTEMD=1 LINUX_ACC_ROOT=1 \
	go test -v -cover -timeout 30m ./internal/provider/

.PHONY: fmt lint test testacc build install generate testenv-up testenv-down testacc-ssh
