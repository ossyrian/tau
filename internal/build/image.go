package build

import (
	"fmt"
	"strings"
)

// Marker is the line in the repo Dockerfile that Assemble replaces with the
// rendered packages. It must be alone on its line.
const Marker = "# @tau:packages"

// LabelConfigHash is the image label that records ConfigHash of the plan the
// image was built from.
const LabelConfigHash = "tau.config-hash"

func assembleSection(dockerfile, section string) (string, error) {
	lines := strings.SplitAfter(dockerfile, "\n")
	at := -1
	for i, l := range lines {
		if strings.TrimRight(l, "\r\n") != Marker {
			continue
		}
		if at >= 0 {
			return "", fmt.Errorf("Dockerfile has more than one %q line", Marker)
		}
		at = i
	}
	if at < 0 {
		return "", fmt.Errorf("Dockerfile has no %q line", Marker)
	}
	lines[at] = section
	return strings.Join(lines, ""), nil
}

// Options describe one `docker build`.
type Options struct {
	Tag     string
	Hash    string   // ConfigHash of the plan, stored as an image label
	Context string   // build context directory
	Extra   []string // extra docker build flags, passed through
}

// DockerArgs returns the arguments for `docker build` reading the Dockerfile
// from stdin.
func DockerArgs(o Options) []string {
	args := []string{"build", "-f", "-", "-t", o.Tag, "--label", LabelConfigHash + "=" + o.Hash}
	args = append(args, o.Extra...)
	return append(args, o.Context)
}
