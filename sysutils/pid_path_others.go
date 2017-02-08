// Copyright 2016 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

// +build !darwin

package sysutils

type NotImplementedError struct{}

func GetExecPathFromPID(pid int) (string, error) {
	return "", NotImplementedError{}
}
