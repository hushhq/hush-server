import { useCallback, useEffect, useRef, useState } from 'react';
import { changePassword } from './lib/adminApi.js';

const OVERLAY_STYLE = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0, 0, 0, 0.6)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 1000,
};

const DIALOG_STYLE = {
  background: 'var(--surface)',
  border: '1px solid var(--border)',
  borderRadius: 'var(--radius-md)',
  padding: '24px',
  width: '360px',
  maxWidth: 'calc(100vw - 32px)',
  display: 'flex',
  flexDirection: 'column',
  gap: '16px',
};

const HEADER_STYLE = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
};

const TITLE_STYLE = {
  fontSize: '0.95rem',
  fontWeight: 600,
  color: 'var(--text)',
};

const CLOSE_BTN_STYLE = {
  background: 'none',
  border: 'none',
  color: 'var(--text-muted)',
  cursor: 'pointer',
  fontSize: '1.2rem',
  lineHeight: 1,
  padding: '2px 6px',
};

const ERROR_STYLE = {
  color: 'var(--danger)',
  fontSize: '0.82rem',
};

const SUCCESS_STYLE = {
  color: 'var(--success)',
  fontSize: '0.82rem',
};

const FORM_STYLE = {
  display: 'flex',
  flexDirection: 'column',
  gap: '10px',
};

const ACTIONS_STYLE = {
  display: 'flex',
  gap: '8px',
  justifyContent: 'flex-end',
};

const EMPTY_FORM = {
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
};

export default function ChangePasswordModal({ isOpen, onClose }) {
  const [form, setForm] = useState(EMPTY_FORM);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);

  // Keep the ref in sync so the Escape handler always sees the current value.
  submittingRef.current = submitting;

  const resetAndClose = useCallback(() => {
    setForm(EMPTY_FORM);
    setError('');
    setSuccess(false);
    setSubmitting(false);
    submittingRef.current = false;
    onClose();
  }, [onClose]);

  // Escape to close — blocked during in-flight submit.
  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (event) => {
      if (event.key === 'Escape' && !submittingRef.current) {
        resetAndClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, resetAndClose]);

  const handleBackdropClick = (event) => {
    if (event.target === event.currentTarget && !submitting) {
      resetAndClose();
    }
  };

  const handleChange = (field) => (event) => {
    setForm((prev) => ({ ...prev, [field]: event.target.value }));
    setError('');
  };

  const handleSubmit = async (event) => {
    event.preventDefault();
    setError('');

    if (form.newPassword !== form.confirmPassword) {
      setError('New passwords do not match.');
      return;
    }
    if (form.newPassword.length < 12) {
      setError('New password must be at least 12 characters.');
      return;
    }

    setSubmitting(true);
    try {
      await changePassword({
        currentPassword: form.currentPassword,
        newPassword: form.newPassword,
      });
      setSuccess(true);
      setTimeout(resetAndClose, 1200);
    } catch (cause) {
      setError(cause.message || 'Password change failed.');
    } finally {
      setSubmitting(false);
    }
  };

  if (!isOpen) return null;

  const canSubmit = form.currentPassword && form.newPassword && form.confirmPassword && !submitting && !success;

  return (
    <div style={OVERLAY_STYLE} onClick={handleBackdropClick}>
      <div style={DIALOG_STYLE} role="dialog" aria-modal="true" aria-labelledby="change-password-title">
        <div style={HEADER_STYLE}>
          <span id="change-password-title" style={TITLE_STYLE}>Change password</span>
          <button
            type="button"
            style={CLOSE_BTN_STYLE}
            onClick={resetAndClose}
            disabled={submitting}
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <form style={FORM_STYLE} onSubmit={handleSubmit}>
          <input
            className="input"
            type="password"
            placeholder="Current password"
            value={form.currentPassword}
            onChange={handleChange('currentPassword')}
            autoComplete="current-password"
            autoFocus
            disabled={submitting || success}
          />
          <input
            className="input"
            type="password"
            placeholder="New password (min 12 chars)"
            value={form.newPassword}
            onChange={handleChange('newPassword')}
            autoComplete="new-password"
            disabled={submitting || success}
          />
          <input
            className="input"
            type="password"
            placeholder="Confirm new password"
            value={form.confirmPassword}
            onChange={handleChange('confirmPassword')}
            autoComplete="new-password"
            disabled={submitting || success}
          />

          {error && <div style={ERROR_STYLE}>{error}</div>}
          {success && <div style={SUCCESS_STYLE}>Password changed successfully.</div>}

          <div style={ACTIONS_STYLE}>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={resetAndClose}
              disabled={submitting}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={!canSubmit}
            >
              {submitting ? 'Saving...' : 'Save password'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
