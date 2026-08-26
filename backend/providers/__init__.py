"""Provider layer. Every other package here has an __init__.py; this one
was missing, and the tree worked anyway as a namespace package -- which
made it cleanup, not a bug. B6 added it so `providers` is a regular
package like its own subpackages already were.
"""
