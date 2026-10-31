package main

import "errors"

var errNotFound = errors.New("session not found")
var errConflict = errors.New("code already in use")
