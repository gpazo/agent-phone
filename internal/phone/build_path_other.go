//go:build !darwin

package phone

func canonicalExistingPath(path string) (string, bool, error) {
	return path, true, nil
}
