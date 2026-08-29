package upgrade

import (
	"fmt"
	"os"
)

// SocketPath is where the Docker daemon listens, and the only way a container
// can replace itself. A variable so that a test can point it somewhere it can
// create a file.
var SocketPath = "/var/run/docker.sock"

// CanApply reports whether this device can replace itself with a newer
// release, and when it cannot, what would have to change.
//
// Two things are required and neither implies the other. Mounting the socket
// without setting allowApply is somebody who wanted the daemon to see Docker
// for another reason; setting allowApply without the socket is somebody who
// meant to and has not restarted the container with the mount yet. Saying
// which one is missing is the difference between a person fixing it in a
// minute and a person giving up.
func CanApply(allowApply bool) (bool, string) {
	_, err := os.Stat(SocketPath)
	haveSocket := err == nil

	switch {
	case allowApply && haveSocket:
		return true, ""
	case !allowApply && !haveSocket:
		return false, fmt.Sprintf(
			"upgrade.allowApply is not set in cue.yaml, and %s is not in this container. "+
				"Both are needed: the setting says you meant it, the socket is how it is done.",
			SocketPath)
	case !allowApply:
		return false, "upgrade.allowApply is not set in cue.yaml"
	default:
		return false, fmt.Sprintf(
			"%s is not in this container. Start it with "+
				"-v %s:%s to let it replace itself.",
			SocketPath, SocketPath, SocketPath)
	}
}

// Image is the registry this program is published to.
const Image = "ghcr.io/" + Repository

// ImageFor names the image for a version, or the moving tag when there is no
// version to name.
func ImageFor(version string) string {
	if version == "" {
		return Image + ":latest"
	}
	return Image + ":" + version
}
