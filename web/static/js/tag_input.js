document.addEventListener('alpine:init', () => {
    Alpine.data('tagInput', (dataId) => ({
        tags: [],
        inputValue: '',

        init() {
            const decodeHtml = (html) => {
                const txt = document.createElement("textarea");
                txt.innerHTML = html;
                return txt.value;
            };
            
            const rawData = document.getElementById(dataId).textContent;
            this.tags = JSON.parse(decodeHtml(rawData));

            this.$watch('tags', value => {
                this.$refs.hiddenInput.value = value.join(',');
            });
        },

        addTag() {
            if (this.inputValue.trim() !== '') {
                const newTags = this.inputValue.split(',').map(t => t.trim().toLowerCase()).filter(t => t !== '' && !this.tags.includes(t));
                this.tags = [...this.tags, ...newTags];
                this.inputValue = '';
            }
        },

        removeTag(index) {
            this.tags = this.tags.filter((_, i) => i !== index);
        }
    }));
});
