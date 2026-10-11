# Terraform Provider Linux

Manage Linux hosts with Terraform, the way Ansible or Salt do: files, packages, users, services, cron jobs, kernel parameters and more, either on the machine running Terraform or on a remote host over SSH.

Everything runs through `/bin/sh` with standard tools, so the managed host needs no agent.

## Features

| Kind | Name | Purpose | Ansible / Salt equivalent |
|------|------|---------|---------------------------|
| Resource | `linux_file` | File content (text or binary), mode, owner and group. Replaced atomically. | `copy`, `template` / `file.managed` |
| Resource | `linux_directory` | Directory, mode, owner and group. | `file state=directory` / `file.directory` |
| Resource | `linux_symlink` | Symbolic link. | `file state=link` / `file.symlink` |
| Resource | `linux_file_line` | A line in a file, replacing the line matching a regular expression. | `lineinfile` / `file.line` |
| Resource | `linux_file_block` | A block of lines between markers. | `blockinfile` / `file.blockreplace` |
| Resource | `linux_patch` | Apply a unified diff, revert it on destroy. No `patch` binary needed on the host. | `patch` / `file.patch` |
| Resource | `linux_sed` | Edit a file in place with `sed` expressions. | `replace` / `file.replace` |
| Resource | `linux_package` | Install a package with apt, dnf, yum, zypper, apk or pacman. | `package` / `pkg.installed` |
| Resource | `linux_user`, `linux_group` | User accounts and groups. | `user`, `group` / `user.present`, `group.present` |
| Resource | `linux_ssh_authorized_key` | Authorize a public key for a user. | `authorized_key` / `ssh_auth.present` |
| Resource | `linux_cron_job` | A job in a user's crontab. | `cron` / `cron.present` |
| Resource | `linux_sysctl` | A kernel parameter, now and after reboots. | `sysctl` / `sysctl.present` |
| Resource | `linux_hostname` | The host name. | `hostname` |
| Resource | `linux_systemd_unit` | Enable, start, mask a unit; restart or reload it when its configuration changes. | `systemd_service` / `service.running` |
| Resource | `linux_command` | Scripts run on create, read, update and destroy. | `command`, `shell` / `cmd.run` |
| Data source | `linux_stat` | Whether a path exists, and its type, mode and owner. | `stat` / `file.exists` |
| Data source | `linux_file` | Content of a file. | `slurp` |
| Data source | `linux_sed` | Whether `sed` expressions would change a file. | `replace` in check mode |
| Data source | `linux_host` | OS, kernel, architecture, CPUs, memory, package manager. | `setup` / grains |
| Data source | `linux_package`, `linux_user`, `linux_group` | Whether a package, user or group exists, and its details. | `package_facts`, `getent` |
| Data source | `linux_systemd_unit` | State of a unit. | `service_facts` |
| Data source | `linux_command` | Output of a command. | `command` |
| Action | `linux_command`, `linux_systemctl` | Run a command or a systemctl command from a `lifecycle.action_trigger` (Terraform 1.14+). | handlers |

The provider connects in one of two modes:

* `local`: commands run on the machine running Terraform.
* `ssh`: commands run on a remote host, with password, private key or agent authentication, host key verification, an optional bastion and `sudo` (with or without a password).

## Usage

```hcl
terraform {
  required_providers {
    linux = {
      source = "labbs/linux"
    }
  }
}

provider "linux" {
  mode = "ssh"
  sudo = true

  ssh = {
    host        = "10.0.0.10"
    user        = "deploy"
    private_key = file("~/.ssh/id_ed25519")
  }
}

resource "linux_file" "nginx_site" {
  path    = "/etc/nginx/conf.d/app.conf"
  content = templatefile("${path.module}/app.conf.tftpl", { server_name = "app.example.com" })
}

resource "linux_systemd_unit" "nginx" {
  name           = "nginx.service"
  enabled        = true
  active         = true
  trigger_action = "reload"

  triggers = {
    config = linux_file.nginx_site.sha256
  }
}
```

The full documentation is in [docs/](docs/index.md) and the examples in [examples/](examples/).

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.5 (actions need >= 1.14)
- [Go](https://golang.org/doc/install) >= 1.25 to build the provider

## Development

```shell
make build      # build the provider
make install    # install it in $GOPATH/bin
make lint       # run golangci-lint
make generate   # format the examples and generate the documentation
make test       # unit tests
```

### Acceptance tests

Acceptance tests run real Terraform commands against a host described by the `LINUX_*` environment variables (see [docs/index.md](docs/index.md)). Most of them need a Linux host with systemd.

`testenv/` holds a Debian container with systemd and an SSH server, published on `127.0.0.1:2222`:

```shell
make testenv-up     # build and start the container (podman by default)
make testacc-ssh    # run the acceptance tests over SSH, with sudo and a password
make testenv-down   # remove the container
```

With Docker, systemd needs extra flags:

```shell
make testenv-up TESTENV_ENGINE=docker \
  TESTENV_RUN_FLAGS="--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw"
```

On a Linux machine, `make testacc` runs the tests locally (`mode = "local"`). Set `LINUX_SUDO=true` and `LINUX_ACC_SYSTEMD=1` to include the tests that need root and systemd. Tests that change the host system-wide (packages, users, kernel parameters, host name...) only run with `LINUX_ACC_ROOT=1`: keep it for throwaway hosts such as the test container.

To try the provider from Terraform without publishing it, add a [development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers) pointing `labbs/linux` to `$GOPATH/bin` after `make install`.
