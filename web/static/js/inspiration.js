document.addEventListener('alpine:init', () => {
    Alpine.data('inspirationApp', () => ({
        selectedTags: [],
        toggleTag(tag) {
            if (this.selectedTags.includes(tag)) {
                this.selectedTags = this.selectedTags.filter(t => t !== tag);
            } else {
                this.selectedTags.push(tag);
            }
        },
        copyPrompt(id) {
            const params = new URLSearchParams();
            if (this.selectedTags.length > 0) {
                params.set('tags', this.selectedTags.join(','));
            }
            const queryString = params.toString();
            const url = '/inspiration/' + encodeURIComponent(id) + '/generate' + (queryString ? '?' + queryString : '');

            return fetch(url)
                .then(response => {
                    if (!response.ok) throw new Error('Network response was not ok');
                    return response.text();
                })
                .then(text => {
                    return window.copyTextSafe(text);
                })
                .catch(err => {
                    console.error('Failed to copy: ', err);
                    if (window.showToast) {
                        window.showToast('Failed to generate or copy prompt. See console.', 'alert-error');
                    } else {
                        alert('Failed to generate prompt. See console.');
                    }
                    throw err;
                });
        }
    }));
});
