// Global Toast Notification & Clipboard Helpers
// Replicates the visual style of web/components/toast.templ for client-side events.

(function() {
    const ALLOWED_TOAST_TYPES = new Set([
        'alert-info',
        'alert-success',
        'alert-warning',
        'alert-error'
    ]);

    window.showToast = function(message, type = 'alert-info') {
        let container = document.getElementById('toast-container');

        // If container doesn't exist (unlikely if using base layout), create it
        if (!container) {
            container = document.createElement('div');
            container.id = 'toast-container';
            container.className = 'toast toast-top toast-end z-[100]';
            document.body.appendChild(container);
        }

        const safeType = ALLOWED_TOAST_TYPES.has(type) ? type : 'alert-info';

        const el = document.createElement('div');
        el.setAttribute('role', 'alert');
        el.className = `alert shadow-lg mb-2 ${safeType}`;

        // Inline styles for transition since we aren't using Alpine's x-transition directly
        // on this dynamically injected element without a wrapper component.
        el.style.transition = 'all 0.3s ease-in-out';
        el.style.opacity = '0';
        el.style.transform = 'scale(0.9)';

        const span = document.createElement('span');
        span.textContent = message == null ? '' : String(message);

        const closeBtn = document.createElement('button');
        closeBtn.type = 'button';
        closeBtn.className = 'btn btn-ghost btn-xs btn-circle';
        closeBtn.textContent = '✕';

        el.appendChild(span);
        el.appendChild(closeBtn);
        container.appendChild(el);

        // Trigger enter animation
        requestAnimationFrame(() => {
            el.style.opacity = '1';
            el.style.transform = 'scale(1)';
        });

        let removed = false;
        const remove = () => {
            if (removed) return;
            removed = true;
            el.style.opacity = '0';
            el.style.transform = 'scale(0.9)';
            setTimeout(() => {
                if (el.parentElement) el.parentElement.removeChild(el);
            }, 300);
        };

        closeBtn.addEventListener('click', remove);

        // Auto-dismiss after 5 seconds
        setTimeout(remove, 5000);
    };

    function fallbackCopyText(value) {
        return new Promise((resolve, reject) => {
            // If a modal <dialog> is open, document.body is inert in the top layer.
            const host = document.querySelector('dialog[open]') || document.body;
            if (!host) {
                reject(new Error('Clipboard copy unavailable: no DOM container'));
                return;
            }

            const textarea = document.createElement('textarea');
            textarea.value = value;
            textarea.setAttribute('readonly', '');
            textarea.setAttribute('aria-hidden', 'true');
            textarea.style.position = 'fixed';
            textarea.style.top = '-9999px';
            textarea.style.left = '-9999px';
            textarea.style.opacity = '0';

            const activeElement = document.activeElement;
            try {
                host.appendChild(textarea);
                textarea.focus({ preventScroll: true });
                textarea.select();
                textarea.setSelectionRange(0, textarea.value.length);

                const ok = document.execCommand('copy');
                if (ok) {
                    resolve();
                } else {
                    reject(new Error('Fallback clipboard copy failed'));
                }
            } catch (err) {
                reject(err);
            } finally {
                if (textarea.parentNode) {
                    textarea.parentNode.removeChild(textarea);
                }
                if (activeElement && typeof activeElement.focus === 'function') {
                    try {
                        activeElement.focus({ preventScroll: true });
                    } catch (_) {
                        // Ignore focus restoration errors
                    }
                }
            }
        });
    }

    // LAN-HTTP-safe clipboard helper returning a Promise.
    window.copyTextSafe = function(text) {
        const value = text == null ? '' : String(text);
        if (
            window.isSecureContext &&
            typeof navigator !== 'undefined' &&
            navigator.clipboard &&
            typeof navigator.clipboard.writeText === 'function'
        ) {
            return navigator.clipboard.writeText(value).catch(() => fallbackCopyText(value));
        }
        return fallbackCopyText(value);
    };
})();
