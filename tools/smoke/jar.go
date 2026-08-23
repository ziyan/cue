package main

import (
	"net/http"
	"net/http/cookiejar"
)

// cookieJar keeps the session cookie the setup step returns, so that every
// request afterwards is authenticated the way a browser's would be.
func cookieJar() (http.CookieJar, error) {
	return cookiejar.New(nil)
}
