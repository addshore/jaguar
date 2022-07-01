// Copyright (C) 2021 Toitware ApS. All rights reserved.
// Use of this source code is governed by an MIT-style license that can be
// found in the LICENSE file.

package main

import (
	"context"
	"os"

	"github.com/toitlang/jaguar/cmd/jag/commands"
	"github.com/toitlang/jaguar/cmd/jag/directory"
)

var (
	date       = "2022-07-01T09:43:08Z"
	version    = "v1.3.0"
	sdkVersion = "v2.0.0-alpha.11"
)

var buildMode = "development"

func main() {
	isReleaseBuild := buildMode == "release"
	directory.IsReleaseBuild = isReleaseBuild

	info := commands.Info{
		Date:       date,
		Version:    version,
		SDKVersion: sdkVersion,
	}
	ctx := commands.SetInfo(context.Background(), info)
	if err := commands.JagCmd(info, isReleaseBuild).ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
