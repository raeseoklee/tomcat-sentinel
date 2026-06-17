//go:build windows

package process

func signalAlive(_ int) (bool, error) {
	return false, nil
}
