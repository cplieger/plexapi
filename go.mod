module github.com/cplieger/plexapi

go 1.26.5

require github.com/cplieger/httpx/v4 v4.2.0

require github.com/cplieger/xmlx v1.0.0

// v1.0.0 shipped same-day API that v1.1.0 reshapes: IsFatalStartup was
// renamed IsConfigError and the read caps became configurable options.
// Nothing external consumed v1.0.0; use v1.1.0 or later.
retract v1.0.0
