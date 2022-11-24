package common

import (
	"fmt"
	"strings"

	"github.com/labbs/terraform-provider-linux/pkg/client"
)

func GetFileFolderDetails(c *client.Client, path string) (userGroup string, mode int, err error) {
	permsMapping := map[string]int{
		"---": 0,
		"--x": 1,
		"-w-": 2,
		"-wx": 3,
		"r--": 4,
		"r-x": 5,
		"rw-": 6,
		"rwx": 7,
	}

	command := fmt.Sprintf("ls -ld %s", path)
	stdout, _, err := client.Command(c, false, command, "")
	if err != nil {
		return "", 0, err
	}

	if stdout == "" {
		return "", 0, fmt.Errorf("file or folder not found")
	}

	parseStdout := strings.Fields(stdout)
	mode = permsMapping[parseStdout[0][1:4]]*100 + permsMapping[parseStdout[0][4:7]]*10 + permsMapping[parseStdout[0][7:10]]
	userGroup = parseStdout[2] + ":" + parseStdout[3]

	return
}
