package external

import (
	"io"
	"net"
	"sync"
)

func startBridgeRelay(sockPath string, bridgeW *io.PipeWriter, clientR *io.PipeReader) (net.Listener, error) {
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}

	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			defer bridgeW.Close()
			io.Copy(bridgeW, conn)
		}()

		go func() {
			defer wg.Done()
			io.Copy(conn, clientR)
		}()

		wg.Wait()
	}()

	return ln, nil
}
