## 0.1.0 (Unreleased)

FEATURES:

* **Provider:** run commands on the machine running Terraform (`mode = "local"`) or on a remote host over SSH (`mode = "ssh"`), with password, private key or agent authentication, host key verification, bastion hosts and `sudo` (with or without a password).
* **New Resource:** `linux_file`
* **New Resource:** `linux_directory`
* **New Resource:** `linux_patch`
* **New Resource:** `linux_sed`
* **New Resource:** `linux_systemd_unit`
* **New Resource:** `linux_command`
* **New Resource:** `linux_file_line`
* **New Resource:** `linux_file_block`
* **New Resource:** `linux_symlink`
* **New Resource:** `linux_package`
* **New Resource:** `linux_user`
* **New Resource:** `linux_group`
* **New Resource:** `linux_ssh_authorized_key`
* **New Resource:** `linux_cron_job`
* **New Resource:** `linux_sysctl`
* **New Resource:** `linux_hostname`
* **New Data Source:** `linux_file`
* **New Data Source:** `linux_command`
* **New Data Source:** `linux_systemd_unit`
* **New Data Source:** `linux_stat`
* **New Data Source:** `linux_sed`
* **New Data Source:** `linux_host`
* **New Data Source:** `linux_package`
* **New Data Source:** `linux_user`
* **New Data Source:** `linux_group`
* **New Action:** `linux_command` (Terraform 1.14+)
* **New Action:** `linux_systemctl` (Terraform 1.14+)
