<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;
use Illuminate\Database\Eloquent\Relations\HasMany;

class User extends Model
{
    protected $fillable = ['email_address', 'first_name'];

    protected $casts = [
        'email_verified_at' => 'datetime',
    ];

    /**
     * Modern accessor (Laravel 9+). Method name is camelCase;
     * the exposed attribute name is its snake_case equivalent: "email_address".
     */
    public function emailAddress(): Attribute
    {
        return Attribute::make(
            get: fn($value) => strtolower($value),
        );
    }

    /**
     * Example of a legacy accessor (pre-Laravel 9).
     * Exposed attribute name: "first_name".
     */
    public function getFirstNameAttribute(string $value): string
    {
        return ucwords($value);
    }

    public function posts(): HasMany
    {
        return $this->hasMany(Post::class);
    }

    /**
     * Get or set the calculated price.
     *
     * @return Attribute<string|null, string|null>
     */
    protected function price(): Attribute
    {
        return Attribute::make(
            get: fn (): ?string => $this->raw_price,
            set: fn (?string $value): void => $this->raw_price = $value,
        );
    }

    public function greet(): string
    {
        // $this->email_address triggers Feature B: jump to emailAddress()
        return 'hi ' . $this->email_address;
    }

    public function formattedPrice(): string
    {
        return '$' . $this->price;
    }
}
