// Package access reads a profile's human-managed access.json and decides which
// chats an agent may see. A file system agent running as the same user can
// change that file; this package cannot protect against that agent.
package access
