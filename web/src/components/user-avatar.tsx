/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMemo } from 'react'

import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import {
  getUserAvatarFallback,
  getUserAvatarStyle,
  resolveUserAvatarUrl,
} from '@/lib/avatar'
import { cn } from '@/lib/utils'

interface UserAvatarProps {
  /** Name the avatar belongs to; drives the initials fallback and its color */
  name: string
  /** Provider avatar address; ignored when it is not a usable https URL */
  avatarUrl?: unknown
  className?: string
  fallbackClassName?: string
}

/**
 * Avatar of a user account: the provider picture when there is a usable address,
 * otherwise the initials block that every account showed before pictures existed.
 *
 * Base UI's Avatar renders the fallback when the image is missing, still loading
 * after an error, so a broken address never leaves an empty slot.
 */
export function UserAvatar(props: UserAvatarProps) {
  const avatarUrl = useMemo(
    () => resolveUserAvatarUrl(props.avatarUrl),
    [props.avatarUrl]
  )
  const fallback = getUserAvatarFallback(props.name)
  const fallbackStyle = useMemo(
    () => getUserAvatarStyle(props.name),
    [props.name]
  )

  return (
    <Avatar className={props.className}>
      {avatarUrl ? (
        <AvatarImage
          src={avatarUrl}
          alt={props.name}
          referrerPolicy='no-referrer'
        />
      ) : null}
      <AvatarFallback
        className={cn('font-semibold text-white', props.fallbackClassName)}
        style={fallbackStyle}
      >
        {fallback}
      </AvatarFallback>
    </Avatar>
  )
}
