package gym

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
)

const DevSalt = "seedfall-gym-dev-salt"

func Salt(env string, dev bool) ([]byte, error) {
	if v := os.Getenv(env); v != "" {
		return []byte(v), nil
	}
	if dev {
		return []byte(DevSalt), nil
	}
	return nil, fmt.Errorf("salt: environment variable %s is empty and -dev is not set", env)
}

func HiddenSeeds(salt []byte, taskID string, n int) []uint64 {
	seeds := make([]uint64, n)
	for i := range seeds {
		mac := hmac.New(sha256.New, salt)
		mac.Write([]byte(taskID))
		mac.Write([]byte{0})
		mac.Write(binary.BigEndian.AppendUint64(nil, uint64(i)))
		seeds[i] = binary.BigEndian.Uint64(mac.Sum(nil)[:8])
	}
	return seeds
}
