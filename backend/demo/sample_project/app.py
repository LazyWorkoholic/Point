"""A tiny demo Flask-less app, shipped with Point's Demo mode."""


def greet(name):
    """Return a friendly greeting for name."""
    if not name:
        name = "friend"
    return f"Hello, {name}!"


def add(a, b):
    """Return the sum of a and b."""
    return a + b


def main():
    print(greet("World"))
    print(add(2, 3))


if __name__ == "__main__":
    main()
