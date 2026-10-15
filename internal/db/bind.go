package db

import (
	"crypto/rand"
	"encoding/hex"
)

// Bind records origin as profile id's extension origin and returns its
// token, creating one if it has none. Running it again keeps the token, so
// a profile bound before stays bound. It holds the global lock, so a round
// that read the file before cannot write the old profile block back.
func Bind(p Paths, id, origin string) (string, error) {
	unlock, err := Lock(p.GlobalLock())
	if err != nil {
		return "", err
	}
	defer unlock()
	d, err := LoadDB(p.ProfileFile(id))
	if err != nil {
		return "", err
	}
	if d.Profile.Origin == origin && d.Profile.Token != "" {
		return d.Profile.Token, nil
	}
	if d.Profile.Token == "" {
		b := make([]byte, 32)
		rand.Read(b) // never fails
		d.Profile.Token = hex.EncodeToString(b)
	}
	d.Profile.Origin = origin
	return d.Profile.Token, SaveDB(p.ProfileFile(id), d)
}
