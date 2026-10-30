"""Python SDK for Tacet plugins.

The SDK wraps the language-neutral protocol in protocol/schema/v1: framing,
initialisation, streaming with credit, cancellation and access through
controlled resource references. Messages are JSON only; Python objects,
pickle and Python exceptions never cross the process boundary.
"""

__version__ = "0.1.0"
PROTOCOL_VERSION = "1.0"
