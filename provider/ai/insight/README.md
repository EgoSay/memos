# insight

One explicit, cancellable OpenAI-compatible chat-completions request over the
selected text and recorded dates. The adapter never discovers notes, retrieves
attachments, follows redirects or writes results. It rejects more than 30
sources / 60,000 UTF-8 text bytes rather than silently truncating them, and
validates returned references against the selected resource names.

Tests use real local HTTP servers to check exact input, unselected citations,
overlong input, cancellation and redirects. They do not prove any user's model
credentials or provider-specific behavior; configuration is validated separately
by an actual explicit generation in the application.
