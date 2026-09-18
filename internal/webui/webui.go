// Package webui holds the Desktop's built assets. One binary always compiles the
// embed (M6.10); Assets() returns them only when the Desktop was actually built,
// so ui and cli Mode share one image and Mode is a runtime key (PLAN.md §6.1).
package webui
