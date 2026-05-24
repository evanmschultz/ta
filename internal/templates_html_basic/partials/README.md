# Template Partials

This directory contains reusable HTML template partials that can be included
in other templates via the `{{ template "partials/name.html" . }}` syntax.

Partials are embedded at compile time and are part of the template set loaded
by the `Render` function.