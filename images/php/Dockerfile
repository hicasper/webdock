# syntax=docker/dockerfile:1

FROM php:8.4-fpm-trixie

COPY --from=mlocati/php-extension-installer:latest /usr/bin/install-php-extensions /usr/local/bin/install-php-extensions
RUN install-php-extensions \
        bcmath \
        exif \
        gd \
        intl \
        mysqli \
        pdo_mysql \
        pdo_pgsql \
        pgsql \
        redis \
        sockets \
        zip \
    && rm -f /usr/local/bin/install-php-extensions

COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer
COPY php.ini /usr/local/etc/php/conf.d/zz-webdock.ini
COPY fpm.conf /usr/local/etc/php-fpm.d/zz-webdock.conf

RUN composer --version
EXPOSE 9000
