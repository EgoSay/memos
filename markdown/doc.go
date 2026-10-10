// Package markdown is the markdown engine: parsing memo content into the AST
// in markdown/ast, the memos-specific syntax in markdown/extensions and
// markdown/parser (tags, mentions, math, GFM extras), rendering, and the
// derived memo payload (tags, properties, snippet).
// Punctuation-bearing names use quoted tags, for example #"books/Title:Edition";
// their values and slash hierarchy are preserved while bare tag rules stay fixed.
//
// Layering: markdown may import proto/gen and internal. It must not import
// store, core, or server.
package markdown
