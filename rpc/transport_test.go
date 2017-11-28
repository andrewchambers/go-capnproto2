package rpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"zombiezen.com/go/capnproto2/rpc"
	rpccp "zombiezen.com/go/capnproto2/std/capnp/rpc"
)

func testTransport(t *testing.T, makePipe func() (t1, t2 rpc.Transport, err error)) {
	t.Run("Close", func(t *testing.T) {
		t1, t2, err := makePipe()
		if err != nil {
			t.Fatal("makePipe:", err)
		}
		if err := t1.CloseRecv(); err != nil {
			t.Error("t1.CloseRecv:", err)
		}
		if err := t1.CloseSend(); err != nil {
			t.Error("t1.CloseSend:", err)
		}
		if err := t2.CloseRecv(); err != nil {
			t.Error("t2.CloseRecv:", err)
		}
		if err := t2.CloseSend(); err != nil {
			t.Error("t2.CloseSend:", err)
		}
	})
	t.Run("Send", func(t *testing.T) {
		ctx := context.Background()
		t1, t2, err := makePipe()
		if err != nil {
			t.Fatal("makePipe:", err)
		}
		if err := t1.CloseRecv(); err != nil {
			t.Error("t1.CloseRecv:", err)
		}
		if err := t2.CloseSend(); err != nil {
			t.Error("t2.CloseSend:", err)
		}

		m, send1, release1, err := t1.NewMessage(ctx)
		if err != nil {
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("t1.NewMessage:", err)
		}
		boot, err := m.NewBootstrap()
		if err != nil {
			release1()
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("NewBootstrap:", err)
		}
		boot.SetQuestionId(42)
		m, send2, release2, err := t1.NewMessage(ctx)
		if err != nil {
			release1()
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("t1.NewMessage:", err)
		}
		boot, err = m.NewBootstrap()
		if err != nil {
			release1()
			release2()
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("NewBootstrap:", err)
		}
		boot.SetQuestionId(123)

		// Send/receive first message
		if err := send2(); err != nil {
			release1()
			release2()
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("send2():", err)
		}
		release2()
		r, release, err := t2.RecvMessage(ctx)
		if err != nil {
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("t2.RecvMessage:", err)
		}
		if r.Which() != rpccp.Message_Which_bootstrap {
			t.Errorf("t2.RecvMessage(ctx).Which = %v; want bootstrap", r.Which())
		} else if rboot, err := r.Bootstrap(); err != nil {
			t.Error("t2.RecvMessage(ctx).Bootstrap:", err)
		} else if rboot.QuestionId() != 123 {
			t.Errorf("t2.RecvMessage(ctx).Bootstrap.QuestionID = %d; want 123", rboot.QuestionId())
		}
		release()

		// Send/receive second message
		if err := send1(); err != nil {
			release1()
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("send1():", err)
		}
		release1()
		r, release, err = t2.RecvMessage(ctx)
		if err != nil {
			t1.CloseSend()
			t2.CloseRecv()
			t.Fatal("t2.RecvMessage:", err)
		}
		if r.Which() != rpccp.Message_Which_bootstrap {
			t.Errorf("t2.RecvMessage(ctx).Which = %v; want bootstrap", r.Which())
		} else if rboot, err := r.Bootstrap(); err != nil {
			t.Error("t2.RecvMessage(ctx).Bootstrap:", err)
		} else if rboot.QuestionId() != 42 {
			t.Errorf("t2.RecvMessage(ctx).Bootstrap.QuestionID = %d; want 123", rboot.QuestionId())
		}
		release()

		if err := t2.CloseRecv(); err != nil {
			t.Error("t2.CloseRecv:", err)
		}
		if err := t1.CloseSend(); err != nil {
			t.Error("t1.CloseSend:", err)
		}
	})
	t.Run("CloseRecv", func(t *testing.T) {
		t1, t2, err := makePipe()
		if err != nil {
			t.Fatal("makePipe:", err)
		}

		done := make(chan struct{})
		go func(ctx context.Context) {
			_, release, _ := t1.RecvMessage(ctx)
			t.Log("t1.RecvMessage returned")
			if release != nil {
				release()
			}
			close(done)
		}(context.Background())
		if err := t1.CloseRecv(); err != nil {
			t.Error("t1.CloseRecv:", err)
		}
		tm := time.NewTimer(15 * time.Second)
		defer tm.Stop()
		select {
		case <-done:
		case <-tm.C:
			t.Error("timed out waiting for t1.RecvMessage to return after CloseRecv")
		}

		if err := t1.CloseSend(); err != nil {
			t.Error("t1.CloseSend:", err)
		}
		if err := t2.CloseRecv(); err != nil {
			t.Error("t2.CloseRecv:", err)
		}
		if err := t2.CloseSend(); err != nil {
			t.Error("t2.CloseSend:", err)
		}
	})
}

func TestTCPStreamTransport(t *testing.T) {
	type listenCall struct {
		c   *net.TCPConn
		err error
	}
	makePipe := func() (t1, t2 rpc.Transport, err error) {
		host, err := net.LookupIP("localhost")
		if err != nil {
			return nil, nil, err
		}
		l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: host[0]})
		if err != nil {
			return nil, nil, err
		}
		ch := make(chan listenCall)
		abort := make(chan struct{})
		go func() {
			c, err := l.AcceptTCP()
			select {
			case ch <- listenCall{c, err}:
			case <-abort:
				c.Close()
			}
		}()
		laddr := l.Addr().(*net.TCPAddr)
		c2, err := net.DialTCP("tcp", nil, laddr)
		if err != nil {
			close(abort)
			l.Close()
			return nil, nil, err
		}
		lc := <-ch
		if lc.err != nil {
			c2.Close()
			l.Close()
			return nil, nil, err
		}
		return rpc.NewStreamTransport(lc.c), rpc.NewStreamTransport(c2), nil
	}
	t.Run("ServerToClient", func(t *testing.T) {
		testTransport(t, makePipe)
	})
	t.Run("ClientToServer", func(t *testing.T) {
		testTransport(t, func() (t1, t2 rpc.Transport, err error) {
			t2, t1, err = makePipe()
			return
		})
	})
}