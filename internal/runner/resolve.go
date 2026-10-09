package runner

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var pinnedRE = regexp.MustCompile(`^(.+)@(sha256:[0-9a-f]{64})$`)

// Resolve returns the reference to run and the image digest
// "sha256:<hex>". For "<ref>@sha256:<hex>" it uses a local image whose ID is
// sha256:<hex> if one exists (locally built images have no registry
// digest), else the reference itself (pulled on first use), and the digest
// from the reference. For any other reference (processor test) it inspects
// the local image and returns its ID as digest.
func (r *Runner) Resolve(ctx context.Context, image string) (ref, digest string, err error) {
	if _, err := exec.LookPath(r.cfg.Runtime); err != nil {
		return "", "", fmt.Errorf("%w: container runtime %q: %v", ErrUnavailable, r.cfg.Runtime, err)
	}
	if strings.TrimSpace(image) == "" || strings.HasPrefix(image, "-") {
		return "", "", fmt.Errorf("%w: invalid image reference %q", ErrUnavailable, image)
	}
	if m := pinnedRE.FindStringSubmatch(image); m != nil {
		digest = m[2]
		if id, err := r.imageID(ctx, digest); err == nil && id == digest {
			return digest, digest, nil
		}
		return image, digest, nil
	}
	id, err := r.imageID(ctx, image)
	if err != nil {
		return "", "", fmt.Errorf("%w: image %s: %v", ErrUnavailable, image, err)
	}
	return image, id, nil
}

// imageID returns the ID of a local image as "sha256:<hex>".
func (r *Runner) imageID(ctx context.Context, ref string) (string, error) {
	out, err := r.output(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if !strings.HasPrefix(id, "sha256:") {
		id = "sha256:" + id // podman prints the bare hex
	}
	if !pinnedRE.MatchString("x@" + id) {
		return "", fmt.Errorf("unexpected image id %q", id)
	}
	return id, nil
}

// ensureImage pulls ref unless it is present locally.
func (r *Runner) ensureImage(ctx context.Context, ref string) error {
	if _, err := r.imageID(ctx, ref); err == nil {
		return nil
	}
	if _, err := r.output(ctx, "pull", "--quiet", ref); err != nil {
		return fmt.Errorf("%w: pulling %s: %v", ErrUnavailable, ref, err)
	}
	return nil
}
