package publish

import "context"

// ArtifactWriter writes report and manifest files to the output location
// with exclusive creation and no overwrite. Writing the same name with the
// same digest again succeeds, so event replay is safe.
type ArtifactWriter interface {
	WriteArtifact(ctx context.Context, name string, data []byte) error
}
