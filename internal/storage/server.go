// Package storage implements confined filesystem storage and streaming gRPC clients.
package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const ChunkSize = 64 * 1024
const MaxFileSize int64 = 1 << 30

type Server struct {
	pb.UnimplementedStorageServer
	root     *os.Root
	identity string
}

func NewServer(dir string) (*Server, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err = root.MkdirAll(".staging", 0700); err != nil {
		root.Close()
		return nil, err
	}
	identity, err := storageIdentity(root)
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("storage identity: %w", err)
	}
	return &Server{root: root, identity: identity}, nil
}
func (s *Server) Close() error { return s.root.Close() }

// ValidateKey permits canonical relative paths only. os.Root also confines symlinks.
func ValidateKey(key string) error {
	if key == "" || len(key) > 512 || strings.ContainsAny(key, "\\\x00") || path.IsAbs(key) || path.Clean(key) != key {
		return fmt.Errorf("invalid file key")
	}
	for _, p := range strings.Split(key, "/") {
		if p == "." || p == ".." || strings.HasPrefix(p, ".") {
			return fmt.Errorf("invalid path component")
		}
	}
	return nil
}
func rpcError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return status.Error(codes.NotFound, "file not found")
	}
	return status.Error(codes.Internal, err.Error())
}
func (s *Server) Upload(stream pb.Storage_UploadServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "file header required")
	}
	header := first.GetHeader()
	if header == nil || ValidateKey(header.Key) != nil {
		return status.Error(codes.InvalidArgument, "invalid file header or key")
	}
	expected, err := hex.DecodeString(header.Sha256)
	if err != nil || len(expected) != sha256.Size {
		return status.Error(codes.InvalidArgument, "SHA-256 checksum required")
	}
	// A random temporary name stays inside the confined root.
	seed := make([]byte, 16)
	if _, err = io.ReadFull(rand.Reader, seed); err != nil {
		return rpcError(err)
	}
	temp := ".staging/" + hex.EncodeToString(seed)
	f, err := s.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return rpcError(err)
	}
	defer func() { f.Close(); s.root.Remove(temp) }()
	hash := sha256.New()
	var size int64
	for {
		if err = stream.Context().Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		msg, e := stream.Recv()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		chunk, ok := msg.Payload.(*pb.UploadRequest_Chunk)
		if !ok || len(chunk.Chunk) > ChunkSize {
			return status.Error(codes.InvalidArgument, "expected chunk of at most 64 KiB")
		}
		size += int64(len(chunk.Chunk))
		if size > MaxFileSize {
			return status.Error(codes.ResourceExhausted, "file too large")
		}
		if _, err = io.MultiWriter(f, hash).Write(chunk.Chunk); err != nil {
			return rpcError(err)
		}
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != strings.ToLower(header.Sha256) {
		return status.Error(codes.DataLoss, "checksum mismatch")
	}
	if err = f.Sync(); err != nil {
		return rpcError(err)
	}
	if err = f.Close(); err != nil {
		return rpcError(err)
	}
	if parent := path.Dir(header.Key); parent != "." {
		if err = s.root.MkdirAll(parent, 0700); err != nil {
			return rpcError(err)
		}
	}
	if err = s.root.Rename(temp, header.Key); err != nil {
		return rpcError(err)
	}
	return stream.SendAndClose(&pb.FileInfo{Key: header.Key, Size: size, Sha256: digest})
}
func (s *Server) Download(req *pb.FileRequest, stream pb.Storage_DownloadServer) error {
	if ValidateKey(req.Key) != nil {
		return status.Error(codes.InvalidArgument, "invalid file key")
	}
	f, err := s.root.Open(req.Key)
	if err != nil {
		return rpcError(err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return rpcError(err)
	}
	if !stat.Mode().IsRegular() {
		return status.Error(codes.InvalidArgument, "not a regular file")
	}
	buf := make([]byte, ChunkSize)
	for {
		n, e := f.Read(buf)
		if n > 0 {
			if err = stream.Send(&pb.DownloadResponse{Chunk: buf[:n]}); err != nil {
				return err
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return rpcError(e)
		}
	}
}
func (s *Server) List(_ *pb.ListRequest, stream pb.Storage_ListServer) error {
	return fs.WalkDir(s.root.FS(), ".", func(key string, d fs.DirEntry, err error) error {
		if err != nil {
			return rpcError(err)
		}
		if key != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err = stream.Context().Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		f, e := s.root.Open(key)
		if e != nil {
			return rpcError(e)
		}
		h := sha256.New()
		n, e := io.Copy(h, f)
		f.Close()
		if e != nil {
			return rpcError(e)
		}
		return stream.Send(&pb.FileInfo{Key: key, Size: n, Sha256: hex.EncodeToString(h.Sum(nil))})
	})
}
func (s *Server) Delete(_ctx context.Context, req *pb.FileRequest) (*pb.DeleteResponse, error) {
	if ValidateKey(req.Key) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid file key")
	}
	if err := s.root.Remove(req.Key); err != nil {
		return nil, rpcError(err)
	}
	return &pb.DeleteResponse{}, nil
}
