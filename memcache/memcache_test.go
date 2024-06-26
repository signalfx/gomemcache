/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package memcache provides a client for the memcached cache server.
package memcache

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const testServer = "localhost:11211"

func setup(t *testing.T) bool {
	c, err := net.Dial("tcp", testServer)
	if err != nil {
		t.Skipf("skipping test; no server running at %s", testServer)
	}
	_, err = c.Write([]byte("flush_all\r\n"))
	if err != nil {
		t.Skipf("skipping test; error writing to server at %s", testServer)
	}
	err = c.Close()
	if err != nil {
		t.Skipf("skipping test; error closing write to server at %s", testServer)
	}
	return true
}

func TestLocalhost(t *testing.T) {
	if !setup(t) {
		return
	}
	testWithClient(t, New(testServer))
}

func TestBinary(t *testing.T) {
	if !setup(t) {
		return
	}
	testBinary(t, New(testServer))
}

// Run the memcached binary as a child process and connect to its unix socket.
func TestUnixSocket(t *testing.T) {
	sock := fmt.Sprintf("/tmp/test-gomemcache-%d.sock", os.Getpid())
	cmd := exec.Command("memcached", "-s", sock)
	if err := cmd.Start(); err != nil {
		t.Skipf("skipping test; couldn't find memcached")
		return
	}

	// Wait a bit for the socket to appear.
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(time.Duration(25*i) * time.Millisecond)
	}

	testWithClient(t, New(sock))
	assert.NoError(t, cmd.Process.Kill())
	assert.Error(t, cmd.Wait())
}

func mustSetF(t *testing.T, c *Client) func(*Item) {
	return func(it *Item) {
		if err := c.Set(it); err != nil {
			t.Fatalf("failed to Set %#v: %v", *it, err)
		}
	}
}

func doSetGetAdd(t *testing.T, c *Client) {
	// Set
	foo := &Item{Key: "foo", Value: []byte("fooval"), Flags: 123}
	err := c.Set(foo)
	checkErr(t, err, "first set(foo): %v", err)
	err = c.Set(foo)
	checkErr(t, err, "second set(foo): %v", err)
	// Get
	it, err := c.Get("foo")
	checkErr(t, err, "get(foo): %v", err)
	if it.Key != "foo" {
		t.Errorf("get(foo) Key = %q, want foo", it.Key)
	}
	if string(it.Value) != "fooval" {
		t.Errorf("get(foo) Value = %q, want fooval", string(it.Value))
	}
	if it.Flags != 123 {
		t.Errorf("get(foo) Flags = %v, want 123", it.Flags)
	}

	// Get and set a unicode key
	quxKey := "Hello_世界"
	qux := &Item{Key: quxKey, Value: []byte("hello world")}
	err = c.Set(qux)
	checkErr(t, err, "first set(Hello_世界): %v", err)
	it, err = c.Get(quxKey)
	checkErr(t, err, "get(Hello_世界): %v", err)
	if it.Key != quxKey {
		t.Errorf("get(Hello_世界) Key = %q, want Hello_世界", it.Key)
	}
	if string(it.Value) != "hello world" {
		t.Errorf("get(Hello_世界) Value = %q, want hello world", string(it.Value))
	}

	// Set malformed keys
	malFormed := &Item{Key: "foo bar", Value: []byte("foobarval")}
	err = c.Set(malFormed)
	if err != ErrMalformedKey {
		t.Errorf("set(foo bar) should return ErrMalformedKey instead of %v", err)
	}
	malFormed = &Item{Key: "foo" + string(rune(0x7f)), Value: []byte("foobarval")}
	err = c.Set(malFormed)
	if err != ErrMalformedKey {
		t.Errorf("set(foo<0x7f>) should return ErrMalformedKey instead of %v", err)
	}

	// Add
	bar := &Item{Key: "bar", Value: []byte("barval")}
	err = c.Add(bar)
	checkErr(t, err, "first add(foo): %v", err)
	if err = c.Add(bar); err != ErrNotStored {
		t.Fatalf("second add(foo) want ErrNotStored, got %v", err)
	}
	// Replace
	baz := &Item{Key: "baz", Value: []byte("bazvalue")}
	if err = c.Replace(baz); err != ErrNotStored {
		t.Fatalf("expected replace(baz) to return ErrNotStored, got %v", err)
	}
	err = c.Replace(bar)
	checkErr(t, err, "replaced(foo): %v", err)
}

func doGetMultiDelete(t *testing.T, c *Client) {
	// GetMulti
	m, err := c.GetMulti([]string{"foo", "bar"})
	checkErr(t, err, "GetMulti: %v", err)
	if g, e := len(m), 2; g != e {
		t.Errorf("GetMulti: got len(map) = %d, want = %d", g, e)
	}
	if _, ok := m["foo"]; !ok {
		t.Fatalf("GetMulti: didn't get key 'foo'")
	}
	if _, ok := m["bar"]; !ok {
		t.Fatalf("GetMulti: didn't get key 'bar'")
	}
	if g, e := string(m["foo"].Value), "fooval"; g != e {
		t.Errorf("GetMulti: foo: got %q, want %q", g, e)
	}
	if g, e := string(m["bar"].Value), "barval"; g != e {
		t.Errorf("GetMulti: bar: got %q, want %q", g, e)
	}

	// Delete
	err = c.Delete("foo")
	checkErr(t, err, "Delete: %v", err)
	_, err = c.Get("foo")
	if err != ErrCacheMiss {
		t.Errorf("post-Delete want ErrCacheMiss, got %v", err)
	}

}

func doIncrDecr(t *testing.T, c *Client) {
	mustSet := mustSetF(t, c)
	// Incr/Decr
	mustSet(&Item{Key: "num", Value: []byte("42")})
	n, err := c.Increment("num", 8)
	checkErr(t, err, "Increment num + 8: %v", err)
	if n != 50 {
		t.Fatalf("Increment num + 8: want=50, got=%d", n)
	}
	n, err = c.Decrement("num", 49)
	checkErr(t, err, "Decrement: %v", err)
	if n != 1 {
		t.Fatalf("Decrement 49: want=1, got=%d", n)
	}
	err = c.Delete("num")
	checkErr(t, err, "delete num: %v", err)
	_, err = c.Increment("num", 1)
	if err != ErrCacheMiss {
		t.Fatalf("increment post-delete: want ErrCacheMiss, got %v", err)
	}
	mustSet(&Item{Key: "num", Value: []byte("not-numeric")})
	_, err = c.Increment("num", 1)
	if err == nil || !strings.Contains(err.Error(), "client error") {
		t.Fatalf("increment non-number: want client error, got %v", err)
	}
	// Test Delete All
	err = c.DeleteAll()
	checkErr(t, err, "DeleteAll: %v", err)
	_, err = c.Get("bar")
	if err != ErrCacheMiss {
		t.Errorf("post-DeleteAll want ErrCacheMiss, got %v", err)
	}
}
func checkErr(t *testing.T, err error, format string, args ...interface{}) {
	if err != nil {
		t.Fatalf(format, args...)
	}
}

func testWithClient(t *testing.T, c *Client) {
	doSetGetAdd(t, c)
	doGetMultiDelete(t, c)
	doIncrDecr(t, c)

	testTouchWithClient(t, c)

}

func testBinary(t *testing.T, c *Client) {
	testCases := []struct {
		key   string
		value []byte
		pass  bool
	}{
		{"can haz spaces", []byte("yay spaces"), true},
		{strings.Repeat("a", 251), []byte("nope"), false},
		{"nospaces", []byte("yaynospaces"), true},
		{"largepayload", bytes.Repeat([]byte{0xff}, 80000), true},
	}
	c.Binary = true
	c.Timeout = time.Second
	for _, tc := range testCases {
		i := &Item{Key: tc.key, Value: tc.value}
		err := c.Set(i)
		if tc.pass {
			assert.NoError(t, err)
			resp, err2 := c.Get(tc.key)
			assert.NoError(t, err2)
			assert.Equal(t, tc.value, resp.Value)
		} else {
			assert.Error(t, err)
		}
	}
	i := &Item{Key: "key", Value: []byte("value")}
	err := c.Add(i)
	assert.Equal(t, ErrUnsupported, err)
	_, err = c.GetMulti([]string{})
	assert.Equal(t, ErrUnsupported, err)
	err = c.Delete("key")
	assert.Equal(t, ErrUnsupported, err)
	err = c.DeleteAll()
	assert.Equal(t, ErrUnsupported, err)
	_, err = c.Increment("key", uint64(1))
	assert.Equal(t, ErrUnsupported, err)
	_, err = c.Decrement("key", uint64(1))
	assert.Equal(t, ErrUnsupported, err)

	err = c.Close()
	assert.NoError(t, err)
}

func testTouchWithClient(t *testing.T, c *Client) {
	if testing.Short() {
		t.Log("Skipping testing memcache Touch with testing in Short mode")
		return
	}

	mustSet := mustSetF(t, c)

	const secondsToExpiry = int32(2)

	// We will set foo and bar to expire in 2 seconds, then we'll keep touching
	// foo every second
	// After 3 seconds, we expect foo to be available, and bar to be expired
	foo := &Item{Key: "foo", Value: []byte("fooval"), Expiration: secondsToExpiry}
	bar := &Item{Key: "bar", Value: []byte("barval"), Expiration: secondsToExpiry}

	setTime := time.Now()
	mustSet(foo)
	mustSet(bar)

	for s := 0; s < 3; s++ {
		time.Sleep(time.Duration(1 * time.Second))
		err := c.Touch(foo.Key, secondsToExpiry)
		if nil != err {
			t.Errorf("error touching foo: %v", err.Error())
		}
	}

	_, err := c.Get("foo")
	if err != nil {
		if err == ErrCacheMiss {
			t.Fatalf("touching failed to keep item foo alive")
		} else {
			t.Fatalf("unexpected error retrieving foo after touching: %v", err.Error())
		}
	}

	_, err = c.Get("bar")
	if nil == err {
		t.Fatalf("item bar did not expire within %v seconds", time.Since(setTime).Seconds())
	} else {
		if err != ErrCacheMiss {
			t.Fatalf("unexpected error retrieving bar: %v", err.Error())
		}
	}
}

func TestConnectionClosing(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	assert.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			assert.NoError(t, err)
			conn.Close()
		}
	}()
	c := New(ln.Addr().String())
	c.Binary = true
	err = c.Set(&Item{Key: "key", Value: []byte("value")})
	assert.Empty(t, c.freeconn)
	_, err = c.Get("key")
	assert.Empty(t, c.freeconn)
}
