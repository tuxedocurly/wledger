document.addEventListener('alpine:init', () => {
    Alpine.data('partLinksForm', () => ({
        newLinks: [],
        addLink() { this.newLinks.push({url: '', label: ''}); },
        removeLink(index) { this.newLinks.splice(index, 1); }
    }));
});
