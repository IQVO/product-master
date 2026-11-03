package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Agentic Standard v2 (AS-1): every tool this server registers declares
// exactly one authority level in its Meta under authorityMetaKey; the fleet
// rule is agent-authority (warehouse-docs agents/fleet/agent-authority.md).
// The levels are the strings L1..L4 (contracts G1): L1 reads facts, L2 is a
// pure recommendation, L3 mutates state, L4 is an autonomous write with no
// human gate. This context has no L2 tool (nothing here recommends without
// reading) and NO ADR grants L4, so only the two levels in use are declared
// as constants — the repo's unused checker fails on dead ones.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
	// authorityL3 mutates state; the caller is accountable.
	authorityL3 = "L3"
)

// authorityMeta is the Meta a tool registers to declare its authority level.
// The level is a declaration for callers and reviewers, not access control:
// the surface stays unauthenticated like REST (fleet-wide revert 2026-09-11).
func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }
