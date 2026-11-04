// Quickstart: create a sandbox, run a command, write and read a file, kill it.
//
//	SBBOX_API_KEY=gsk-… go run ./quickstart
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	sbbox "github.com/genspark-ai/sb-box-sdk/go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := sbbox.New() // key from SBBOX_API_KEY (or GSK_API_KEY)
	if err != nil {
		log.Fatal(err)
	}

	sb, err := client.Sandboxes.Create(ctx, sbbox.CreateOptions{Template: "base", Timeout: 600})
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Kill(context.Background())
	fmt.Println("sandbox", sb.ID, sb.State)

	res, err := sb.Exec(ctx, []string{"python3", "-c", "print('hello from sb-box')"}, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("exit=%d stdout=%s", res.ExitCode, res.Stdout)

	if _, err := sb.Files.Write(ctx, "/home/user/note.txt", "written by the Go SDK\n", ""); err != nil {
		log.Fatal(err)
	}
	text, err := sb.Files.ReadText(ctx, "/home/user/note.txt", "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("file: ", text)

	fmt.Println("port 3000 would be served at", sb.PortURL(3000))
}
