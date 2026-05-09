export interface PrototypeNoticeProps {
  title: string;
  message: string;
}

export const PrototypeNotice = ({ title, message }: PrototypeNoticeProps) => (
  <section
    style={{
      border: '1px solid rgba(255, 206, 86, 0.28)',
      background: 'rgba(255, 206, 86, 0.08)',
      borderRadius: 18,
      padding: '0.95rem 1rem',
      color: '#fff7d6',
    }}
  >
    <strong style={{ display: 'block', marginBottom: '0.2rem' }}>{title}</strong>
    <span>{message}</span>
  </section>
);
