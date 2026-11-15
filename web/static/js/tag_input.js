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

            // Flush any pending typed tags synchronously before the enclosing form submits.
            const form = this.$el.closest('form');
            if (form) {
                this._onFormSubmit = () => {
                    this.addTag();
                    this.$refs.hiddenInput.value = this.tags.join(',');
                };
                form.addEventListener('submit', this._onFormSubmit);
            }
        },

        destroy() {
            if (this._onFormSubmit) {
                const form = this.$el.closest('form');
                if (form) {
                    form.removeEventListener('submit', this._onFormSubmit);
                }
            }
        },

        handleKeydown(event) {
            if (event.key === ',') {
                event.preventDefault();
                this.addTag();
            }
        },

        handleInput() {
            if (!this.inputValue.includes(',')) {
                return;
            }
            const parts = this.inputValue.split(',');
            const remainder = parts.pop();
            const committed = parts
                .map(t => t.trim().toLowerCase())
                .filter(t => t !== '' && !this.tags.includes(t));
            if (committed.length > 0) {
                this.tags = [...this.tags, ...new Set(committed)];
                this.$refs.hiddenInput.value = this.tags.join(',');
            }
            this.inputValue = remainder;
        },

        addTag() {
            if (this.inputValue.trim() !== '') {
                const newTags = this.inputValue
                    .split(',')
                    .map(t => t.trim().toLowerCase())
                    .filter(t => t !== '' && !this.tags.includes(t));
                if (newTags.length > 0) {
                    this.tags = [...this.tags, ...new Set(newTags)];
                    this.$refs.hiddenInput.value = this.tags.join(',');
                }
                this.inputValue = '';
            }
        },

        removeTag(index) {
            this.tags = this.tags.filter((_, i) => i !== index);
        }
    }));
});
