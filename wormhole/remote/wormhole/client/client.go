package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	pb "github.com/orbstack/macvirt/wormhole/remote/wormhole/protobuf"
	"google.golang.org/grpc"
)

func main() {
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	client := pb.NewWormholeServiceClient(conn)

	stream, err := client.SendCommand(context.Background())
	if err != nil {
		log.Fatalf("Error creating stream: %v", err)
	}

	go func() {
		for {
			out, err := stream.Recv()
			if err != nil {
				log.Fatalf("Failed to receive output: %v", err)
			}
			// fmt.Printf("Child Process Output: %s\n", out.Output)
			fmt.Println(out.Output)
		}
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		// if !scanner.Scan() {
		// 	break
		// }
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("error reading string %+v\n", err)
			break
		}

		if err := stream.Send(&pb.InputMessage{Input: input}); err != nil {
			log.Fatalf("Failed to send input: %v", err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		log.Fatalf("Failed to close stream: %v", err)
	}
}
