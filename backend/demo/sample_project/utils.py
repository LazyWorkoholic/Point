"""Small helpers used by app.py -- shipped so Demo mode's @research
fixture has a second real file to read."""


def slugify(text):
    """Lowercase text and replace runs of whitespace with a single dash."""
    return "-".join(text.lower().split())


def clamp(value, low, high):
    """Clamp value into the inclusive [low, high] range."""
    return max(low, min(value, high))
