action "linux_command" "migrate" {
  config {
    command           = "./bin/migrate up"
    working_directory = "/opt/app"
  }
}

resource "linux_file" "release" {
  path    = "/opt/app/RELEASE"
  content = var.release

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.linux_command.migrate]
    }
  }
}
