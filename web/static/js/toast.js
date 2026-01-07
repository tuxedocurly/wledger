// Global Toast Notification Helper
// Replicates the visual style of web/components/toast.templ for client-side events.

window.showToast = function(message, type = 'alert-info') {
    let container = document.getElementById('toast-container');
    
    // If container doesn't exist (unlikely if using base layout), create it
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.className = 'toast toast-top toast-end z-[100]';
        document.body.appendChild(container);
    }

    const el = document.createElement('div');
    // Base classes matching DaisyUI alert
    el.className = `alert shadow-lg mb-2 ${type}`;
    
    // Inline styles for transition since we aren't using Alpine's x-transition directly
    // on this dynamically injected element without a wrapper component.
    el.style.transition = 'all 0.3s ease-in-out';
    el.style.opacity = '0'; 
    el.style.transform = 'scale(0.9)';

    el.innerHTML = `
        <span>${message}</span>
        <button class="btn btn-ghost btn-xs btn-circle">✕</button>
    `;

    // Append to container
    container.appendChild(el);

    // Trigger enter animation
    requestAnimationFrame(() => {
        el.style.opacity = '1';
        el.style.transform = 'scale(1)';
    });

    const remove = () => {
        el.style.opacity = '0';
        el.style.transform = 'scale(0.9)';
        setTimeout(() => {
            if (el.parentElement) el.parentElement.removeChild(el);
        }, 300);
    };

    // Attach click handler to close button
    const closeBtn = el.querySelector('button');
    if (closeBtn) {
        closeBtn.addEventListener('click', remove);
    }

    // Auto-dismiss after 5 seconds
    setTimeout(remove, 5000);
};
