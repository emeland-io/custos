// Package match compares the output of a processor run with what the same
// answer produced earlier (spec §5.4) and plans the files of an output
// proposal: new generated tasks and documents, new versions, removals. It
// reads only the trees it is given and never touches Git.
package match
