package chrome

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chriscz/mirrormark/internal/db"
)

// pbField encodes one protobuf length-delimited field.
func pbField(num int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

// WriteCRX packs dir as a CRX3 file signed with key: "Cr24", version 3, the
// header length, a CrxFileHeader protobuf holding the public key, the
// signature and the signed crx_id, then the zip archive. The signature
// covers "CRX3 SignedData\x00", the signed data's length and the signed data,
// and the archive. The file is world-readable: Chrome reads it as the user.
func WriteCRX(dir string, key *rsa.PrivateKey, out string) error {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	pub := PublicKeyDER(key)
	sum := sha256.Sum256(pub)
	signedData := pbField(1, sum[:16]) // SignedData.crx_id
	msg := []byte("CRX3 SignedData\x00")
	msg = binary.LittleEndian.AppendUint32(msg, uint32(len(signedData)))
	msg = append(msg, signedData...)
	msg = append(msg, zbuf.Bytes()...)
	h := sha256.Sum256(msg)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return err
	}
	header := pbField(2, append(pbField(1, pub), pbField(2, sig)...)) // sha256_with_rsa
	header = append(header, pbField(10000, signedData)...)            // signed_header_data
	crx := []byte("Cr24")
	crx = binary.LittleEndian.AppendUint32(crx, 3)
	crx = binary.LittleEndian.AppendUint32(crx, uint32(len(header)))
	crx = append(crx, header...)
	crx = append(crx, zbuf.Bytes()...)
	return db.WriteAtomic(out, crx, 0o644)
}
