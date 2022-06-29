DROP TABLE IF EXISTS `isu_association_config`;
DROP TABLE IF EXISTS `isu_condition`;
DROP TABLE IF EXISTS `isu`;
DROP TABLE IF EXISTS `user`;

CREATE TABLE `isu` (
  `id` bigint AUTO_INCREMENT,
  `jia_isu_uuid` CHAR(36) NOT NULL UNIQUE,
  `name` VARCHAR(255) NOT NULL,
  `image` LONGBLOB,
  `character` VARCHAR(255),
  `jia_user_id` VARCHAR(255) NOT NULL,
  `created_at` DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
   PRIMARY KEY(`id`)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4;

CREATE TABLE `isu_condition` (
  `id` int DEFAULT 0,
  `jia_isu_uuid` CHAR(36) NOT NULL,
  `timestamp` DATETIME NOT NULL,
  `is_sitting` TINYINT(1) NOT NULL,
  `condition` VARCHAR(255) NOT NULL,
  `message` VARCHAR(255) NOT NULL,
  `created_at` DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY(`jia_isu_uuid`, `timestamp`)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4;


CREATE TABLE `user` (
  `jia_user_id` VARCHAR(255) PRIMARY KEY,
  `created_at` DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4;

CREATE TABLE `isu_association_config` (
  `name` VARCHAR(255) PRIMARY KEY,
  `url` VARCHAR(255) NOT NULL UNIQUE
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4;

DROP TABLE IF EXISTS `latest_isu_condition`;
CREATE TABLE `latest_isu_condition` (
    `jia_isu_uuid` CHAR(36) NOT NULL,
    `timestamp` DATETIME NOT NULL,
    `is_sitting` TINYINT(1) NOT NULL,
    `condition` VARCHAR(255) NOT NULL,
    `message` VARCHAR(255) NOT NULL,
    `created_at` DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6),
PRIMARY KEY(`jia_isu_uuid`)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4;

CREATE TRIGGER latest_isu_condition_tr BEFORE INSERT ON `isu_condition` FOR EACH ROW INSERT INTO `latest_isu_condition` VALUES (NEW.jia_isu_uuid, NEW.timestamp, NEW.is_sitting, NEW.message, NEW.created_at)
ON DUPLICATE KEY UPDATE latest_isu_condition.timestamp = NEW.timestamp, latest_isu_condition.is_sitting = NEW.is_sitting, latest_isu_condition.condition = NEW.condition, latest_isu_condition.message = NEW.message, latest_isu_condition.created_at = NEW.created_at;
