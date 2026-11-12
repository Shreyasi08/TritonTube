package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
)

const identityFile = ".node-id"

func storageIdentity(root *os.Root) (string, error) {
	data, err := root.ReadFile(identityFile)
	if err == nil {
		identity := strings.TrimSpace(string(data))
		raw, e := hex.DecodeString(identity)
		if e != nil || len(raw) != 16 {
			return "", fmt.Errorf("invalid persisted identity in %s", identityFile)
		}
		return identity, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	identity := hex.EncodeToString(raw)
	file, err := root.OpenFile(identityFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		return storageIdentity(root)
	}
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(identity + "\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	return identity, nil
}
func (s *Server) Info(_ context.Context, _ *pb.InfoRequest) (*pb.NodeInfo, error) {
	return &pb.NodeInfo{StorageId: s.identity}, nil
}
