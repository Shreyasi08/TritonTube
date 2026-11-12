package storage

import (
	"context"
	"fmt"
	"io"

	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  pb.StorageClient
}

// Dial uses plaintext for the local demo; deploy behind a trusted network only.
func Dial(address string) (*Client, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, rpc: pb.NewStorageClient(conn)}, nil
}
func (c *Client) Close() error { return c.conn.Close() }
func (c *Client) Upload(ctx context.Context, key, checksum string, r io.Reader) (*pb.FileInfo, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.rpc.Upload(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err = stream.Send(&pb.UploadRequest{Payload: &pb.UploadRequest_Header{Header: &pb.FileHeader{Key: key, Sha256: checksum}}}); err != nil {
		return nil, uploadSendError(stream, err)
	}
	buf := make([]byte, ChunkSize)
	for {
		n, e := r.Read(buf)
		if n > 0 {
			if err = stream.Send(&pb.UploadRequest{Payload: &pb.UploadRequest_Chunk{Chunk: buf[:n]}}); err != nil {
				return nil, uploadSendError(stream, err)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
	}
	return stream.CloseAndRecv()
}
func (c *Client) Download(ctx context.Context, key string, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.rpc.Download(ctx, &pb.FileRequest{Key: key})
	if err != nil {
		return err
	}
	for {
		msg, e := stream.Recv()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		n, e := w.Write(msg.Chunk)
		if e != nil {
			return e
		}
		if n != len(msg.Chunk) {
			return io.ErrShortWrite
		}
	}
}
func (c *Client) List(ctx context.Context) ([]*pb.FileInfo, error) {
	stream, err := c.rpc.List(ctx, &pb.ListRequest{})
	if err != nil {
		return nil, err
	}
	result := make([]*pb.FileInfo, 0)
	for {
		f, e := stream.Recv()
		if e == io.EOF {
			return result, nil
		}
		if e != nil {
			return nil, e
		}
		result = append(result, f)
	}
}
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.rpc.Delete(ctx, &pb.FileRequest{Key: key})
	return err
}

func (c *Client) Health(ctx context.Context) error {
	response, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("node is not serving: %s", response.Status)
	}
	return nil
}

// Send can return EOF when the server rejects the stream before the client has
// finished sending. Receive the final status so callers get the actual RPC error.
func uploadSendError(stream pb.Storage_UploadClient, err error) error {
	if err != io.EOF {
		return err
	}
	_, responseErr := stream.CloseAndRecv()
	if responseErr != nil {
		return responseErr
	}
	return io.ErrUnexpectedEOF
}

// Identity identifies the physical storage directory, independently of its
// configured node name and any DNS/address aliases used to reach the process.
func (c *Client) Identity(ctx context.Context) (string, error) {
	info, err := c.rpc.Info(ctx, &pb.InfoRequest{})
	if err != nil {
		return "", err
	}
	if info.StorageId == "" {
		return "", fmt.Errorf("node returned an empty storage identity")
	}
	return info.StorageId, nil
}
