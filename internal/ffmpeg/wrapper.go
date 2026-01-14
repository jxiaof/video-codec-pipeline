package ffmpeg

import (
	"fmt"
	"strings"
)

// BuildCommand 构建FFmpeg命令的函数
func BuildCommand(input string, output string, options map[string]string) string {
	var cmdBuilder strings.Builder

	cmdBuilder.WriteString("ffmpeg -i ")
	cmdBuilder.WriteString(input)

	for key, value := range options {
		cmdBuilder.WriteString(fmt.Sprintf(" -%s %s", key, value))
	}

	cmdBuilder.WriteString(" ")
	cmdBuilder.WriteString(output)

	return cmdBuilder.String()
}