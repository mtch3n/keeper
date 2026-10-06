// Package detect holds keeper's detection passes and the chain that runs a
// connection's passes together. A pass finds PII inside free text and reports
// where; it never decides a column's policy, which belongs to the catalog.
//
// keeper authors no detection of its own. The patterns pass is
// github.com/hoophq/alcatraz, and the list pass matches terms the operator
// supplies.
package detect
