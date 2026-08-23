package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	spotifyTotpSecretRaw = `,7/*F("rLJ2oxaKL^f+E1xvP@N`
	spotifyTotpVersion   = "61"
)

var spotifyTotpSecretKey = func() []byte {
	var parts []string
	for i, c := range []byte(spotifyTotpSecretRaw) {
		parts = append(parts, strconv.Itoa(int(c)^int(i%33+9)))
	}
	return []byte(strings.Join(parts, ""))
}()

// GenerateSpotifyTOTP generates the TOTP code and version required by Spotify's /api/token
func GenerateSpotifyTOTP(timestampMs int64) (string, string) {
	if timestampMs <= 0 {
		timestampMs = time.Now().UnixMilli()
	}

	counter := uint64(timestampMs / 30000)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, spotifyTotpSecretKey)
	mac.Write(buf[:])
	digest := mac.Sum(nil)

	offset := digest[len(digest)-1] & 0x0f
	binaryCode := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	code := binaryCode % 1000000

	return fmt.Sprintf("%06d", code), spotifyTotpVersion
}
