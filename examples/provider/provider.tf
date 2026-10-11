# Remote host over SSH, as root through sudo.
provider "linux" {
  mode = "ssh"
  sudo = true

  ssh = {
    host        = "10.0.0.10"
    user        = "deploy"
    private_key = file("~/.ssh/id_ed25519")

    # Optional jump host.
    bastion = {
      host = "bastion.example.com"
      user = "jump"
    }
  }
}

# The machine running Terraform.
provider "linux" {
  alias = "local"
  mode  = "local"
}
