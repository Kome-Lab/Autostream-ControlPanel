-- Keep the MariaDB stream status enum aligned with the statuses used by the
-- Control Panel waiting/rearm and inactive-edit lifecycle.
ALTER TABLE streams
  MODIFY COLUMN status ENUM('created','draft','scheduled','ready','starting','live','stopping','completed','failed','stopped') NOT NULL;
