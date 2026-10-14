import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import SecurityTab from '@/pages/settings/SecurityTab';
import { HttpUtil } from '@/utils';

describe('API token creation date', () => {
  it('renders both API seconds and legacy millisecond timestamps', async () => {
    const get = vi.spyOn(HttpUtil, 'get');
    // Mount fetches the token list first and the administrator list second.
    get.mockResolvedValueOnce({
      success: true,
      msg: '',
      obj: [
        {
          id: 2,
          name: 'seconds-token',
          enabled: true,
          createdAt: 1782485394,
        },
        {
          id: 3,
          name: 'legacy-milliseconds-token',
          enabled: true,
          createdAt: 1782485394270,
        },
      ],
    });
    get.mockResolvedValueOnce({ success: true, msg: '', obj: [] });

    render(<SecurityTab />);
    fireEvent.click(screen.getByRole('tab', { name: /API Token/ }));

    expect(await screen.findByText('seconds-token')).toBeTruthy();
    expect(screen.getByText('legacy-milliseconds-token')).toBeTruthy();
    expect(screen.getAllByText(/2026/)).toHaveLength(2);
  });
});
