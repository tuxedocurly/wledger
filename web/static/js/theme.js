(function() {
    const savedTheme = localStorage.getItem('theme') || 'dark';
    document.documentElement.setAttribute('data-theme', savedTheme);
})();

document.addEventListener('alpine:init', () => {
    Alpine.data('themeToggle', () => ({
        theme: localStorage.getItem('theme') || 'dark',
        isAnimating: false,
        toggle() {
            this.isAnimating = true;
            this.theme = this.theme === 'dark' ? 'silk' : 'dark';
            localStorage.setItem('theme', this.theme);
            document.documentElement.setAttribute('data-theme', this.theme);
            // Reset animation after it completes (approx 500ms usually for swaps)
            setTimeout(() => this.isAnimating = false, 500); 
        }
    }));
});