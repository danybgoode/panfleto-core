/**
 * Comments collapse/expand functionality
 * Ported from editorial-panfleto's ArticleComments component
 * Provides HackerNews-style collapse/expand for comment threads
 */

// Toggle a single comment's collapsed state
function toggleComment(button) {
    const commentDiv = button.closest('.entry-comment');
    if (!commentDiv) return;

    const body = commentDiv.querySelector('.entry-comment-body');
    const children = commentDiv.querySelector('.entry-comment-children');
    const isCollapsed = button.getAttribute('aria-expanded') === 'false';

    if (isCollapsed) {
        // Expand
        if (body) body.style.display = '';
        if (children) children.style.display = '';
        button.textContent = '[-]';
        button.setAttribute('aria-expanded', 'true');
    } else {
        // Collapse
        if (body) body.style.display = 'none';
        if (children) children.style.display = 'none';
        button.textContent = '[+]';
        button.setAttribute('aria-expanded', 'false');
    }
}

// Global collapse all comments
function collapseAllComments() {
    document.querySelectorAll('.collapse-toggle[aria-expanded="true"]').forEach(btn => {
        toggleComment(btn);
    });
}

// Global expand all comments
function expandAllComments() {
    document.querySelectorAll('.collapse-toggle[aria-expanded="false"]').forEach(btn => {
        toggleComment(btn);
    });
}

// Initialize collapse state based on URL hash
// Allows deep linking to specific comments
function initCommentVisibility() {
    const hash = window.location.hash;
    if (!hash || !hash.startsWith('#comment-')) {
        return;
    }

    // If URL has a comment hash, ensure it's visible
    const targetComment = document.querySelector(hash);
    if (targetComment) {
        // Walk up the tree and expand all ancestors
        let ancestor = targetComment.closest('.entry-comment');
        while (ancestor) {
            const toggle = ancestor.querySelector('.collapse-toggle');
            if (toggle && toggle.getAttribute('aria-expanded') === 'false') {
                toggleComment(toggle);
            }
            ancestor = ancestor.closest('.entry-comment');
        }
        
        // Scroll to the comment
        targetComment.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
}

// Add keyboard shortcuts for collapse/expand
function initKeyboardShortcuts() {
    document.addEventListener('keydown', (e) => {
        // Only handle if not typing in an input
        if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') {
            return;
        }

        // Global shortcuts
        if (e.key === 'c' && e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey) {
            collapseAllComments();
            e.preventDefault();
        }
        if (e.key === 'e' && e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey) {
            expandAllComments();
            e.preventDefault();
        }
    });
}

// Initialize on DOM load
document.addEventListener('DOMContentLoaded', () => {
    initCommentVisibility();
    initKeyboardShortcuts();
});

// Also run if loaded dynamically (for lazy-loaded comments)
if (document.readyState !== 'loading') {
    initCommentVisibility();
    initKeyboardShortcuts();
}
