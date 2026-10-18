package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitContainerdReadyRetries(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitContainerdReady(ctx, time.Nanosecond, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("socket: no such file or directory")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatal(calls, err)
	}
}

func TestWaitContainerdReadyCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := waitContainerdReady(ctx, time.Hour, func(context.Context) error { cancel(); return errors.New("socket missing") })
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "socket missing") {
		t.Fatal(err)
	}
}
