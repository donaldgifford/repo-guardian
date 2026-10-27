// Package v1config reads a repo-guardian v1 guardian.hcl loosely: it reports
// what the file declares without the v1 loader, so it keeps working after
// that loader is deleted.
//
// The reader never evaluates the file. Every value is kept as its source
// text; literal strings, booleans and string lists are additionally decoded
// with no evaluation context, so locals, interpolations and function calls
// stay exactly as written. Blocks and attributes the reader does not know are
// listed in [Document.Unrecognised] with their line rather than failing, so a
// newer v1 policy still renders.
package v1config
