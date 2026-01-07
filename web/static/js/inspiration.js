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
            const tagsQuery = this.selectedTags.length > 0 ? '?tags=' + this.selectedTags.join(',') : '';
            return fetch('/inspiration/' + id + '/generate' + tagsQuery)
                .then(response => {
                    if (!response.ok) throw new Error('Network response was not ok');
                    return response.text();
                })
                .then(text => {
                    return navigator.clipboard.writeText(text);
                })
                .catch(err => {
                    console.error('Failed to copy: ', err);
                    if (window.showToast) {
                        window.showToast('Failed to generate prompt. See console.', 'alert-error');
                    } else {
                        alert('Failed to generate prompt. See console.');
                    }
                    throw err;
                });
        }
    }))
})

