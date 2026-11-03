// Package detect holds keeper's pattern detection. It finds PII inside free
// text and reports where; it never decides a column's policy, which belongs to
// the catalog.
//
// keeper authors no detection of its own: the patterns detector is
// github.com/hoophq/alcatraz.
package detect
