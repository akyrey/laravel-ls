<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class Post extends Model
{
    protected $appends = ['slug_url'];
    protected $hidden = ['secret_token'];

    public function setTitleAttribute(string $value): void
    {
        $this->attributes['title'] = strtolower($value);
    }
}
