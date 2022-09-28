# linkerd-golang-extension

The [Linkerd service mesh](https://linkerd.io) includes a simple but powerful extension
mechanism:

- Anyone can write an extension to the Linkerd CLI simply by including an executable
  named `linkerd-$extension` in their `PATH`; this will be usable as `linkerd $extension`.
  (This is basically the same mechanism as extensions for `kubectl`.)

- Anyone can _also_ write a Kubernetes controller that interacts with the Linkerd CRDs
  for whatever purpose they want. Obviously, this is most useful when coupled with a CLI
  extension.

Buoyant is providing this repository (https://github.com/BuoyantIO/linkerd-golang-extension)
as a tutorial example of how to write a Linkerd extension in Golang.

## Building

You must have Go version 1.19 or higher installed.

- To build, just run `make`. 

   - If needed, you can set `GOARCH` and `GOOS` as needed (e.g. `make GOOS=linux GOARCH=amd64`
     to cross-compile for an x86 Kubernetes environment on a Mac).

- To clean everything up, use `make clean`.

## The CLI Extension

More details about writing a CLI extension are available in [EXTENSIONS.md] in the main
[Linkerd2 repo]. It details the required commands (`install`, `uninstall`, and `check`)
switches. Note that extensions are allowed to implement additional commands, but the
three commands above are _required_.

The sources for the CLI part of this extension are in the `cmd` directory. Start reading
with `cmd/main.go`.

[EXTENSIONS.md]: https://github.com/linkerd/linkerd2/blob/main/EXTENSIONS.md
[Linkerd2 repo]: https://github.com/linkerd/linkerd2/
